// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// contentSecurityPolicy is the webview CSP (UI-0001 section 4). It lives in
// csp.txt so that the Vite build can inject the same policy as a <meta> tag.
//
//go:embed csp.txt
var contentSecurityPolicy string

// Wails runtime object ids, copied from pkg/application/messageprocessor.go
// of the pinned Wails version (v3.1.0). Moving the Wails minor requires
// re-checking this table (UI-0001 section 1).
const (
	runtimeCall        = 0
	runtimeClipboard   = 1
	runtimeApplication = 2
	runtimeEvents      = 3
	runtimeContextMenu = 4
	runtimeDialog      = 5
	runtimeWindow      = 6
	runtimeScreens     = 7
	runtimeSystem      = 8
	runtimeBrowser     = 9
	runtimeCancelCall  = 10
)

// allowedRuntimeObjects is the part of the Wails runtime that scripts in the
// webview may reach. Everything else is refused: Browser (opens arbitrary
// URLs, including custom schemes), Dialog, Clipboard, ContextMenu, Application
// (Quit/Hide/Show) and Screens. Native capabilities are exposed later through
// narrow bound services instead (UI-0001 section 4).
var allowedRuntimeObjects = map[int]bool{
	runtimeCall:       true, // bound services
	runtimeEvents:     true,
	runtimeWindow:     true,
	runtimeSystem:     true,
	runtimeCancelCall: true,
}

const (
	runtimePath = "/wails/runtime"
	// chunkIDHeader marks the chunked upload of an oversized runtime body.
	// Chunks cannot be inspected piece by piece, so they are refused.
	chunkIDHeader = "X-Wails-Chunk-Id"
	// maxRuntimeBody bounds how much of a runtime call body is buffered to
	// read its object id.
	maxRuntimeBody = 4 << 20
)

// securityMiddleware wraps the asset server chain, which Wails runs outermost,
// so it sees every request the webview makes, including POST /wails/runtime:
//
//   - sets the CSP and companion headers on every response (csp is empty in
//     dev mode, where Vite needs inline scripts and a WebSocket);
//   - refuses runtime calls to objects outside allowedRuntimeObjects.
func securityMiddleware(csp string, log *slog.Logger) application.Middleware {
	csp = strings.TrimSpace(csp)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if csp != "" {
				h.Set("Content-Security-Policy", csp)
			}
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")

			if r.URL.Path == runtimePath && !authorizeRuntimeCall(w, r, log) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authorizeRuntimeCall reports whether the runtime call may proceed. On
// refusal it has already written the response. The request body is restored
// for the next handler.
func authorizeRuntimeCall(w http.ResponseWriter, r *http.Request, log *slog.Logger) bool {
	if r.Header.Get(chunkIDHeader) != "" {
		log.Warn("refused chunked runtime call")
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRuntimeBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	object, ok := runtimeObject(r, body)
	if !ok {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return false
	}
	if !allowedRuntimeObjects[object] {
		log.Warn("refused runtime call to a disallowed object", "object", object)
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return false
	}
	return true
}

// runtimeObject extracts the target object id the same way the Wails HTTP
// transport does: from the JSON body, or from the query string when the
// webview sends no body (WebKitGTK 6.0 with custom URI schemes).
func runtimeObject(r *http.Request, body []byte) (int, bool) {
	if len(body) > 0 {
		var req struct {
			Object *int `json:"object"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Object == nil {
			return 0, false
		}
		return *req.Object, true
	}
	object, err := strconv.Atoi(r.URL.Query().Get("object"))
	if err != nil {
		return 0, false
	}
	return object, true
}
