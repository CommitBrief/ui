// SPDX-License-Identifier: GPL-3.0-or-later

package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/port"
)

// viewArgs builds `gh pr view N --repo=o/r --json state,headRefOid,isDraft`.
func viewArgs(repo string, number int) []string {
	return []string{"pr", "view", strconv.Itoa(number), "--repo=" + repo, "--json=state,headRefOid,isDraft"}
}

// ViewPR implements port.PRSource: the state and head commit of one pull
// request, read from GitHub every time (the search index lags, UI-0005 section 3).
func (c *Client) ViewPR(ctx context.Context, repo string, number int) (port.PRInfo, error) {
	if !validRepo(repo) {
		return port.PRInfo{}, invalidArg("repository must look like owner/name")
	}
	if number <= 0 || number > maxPRNumber {
		return port.PRInfo{}, invalidArg("pull request number %d is out of range", number)
	}
	out, err := c.runJSON(ctx, viewArgs(repo, number))
	if err != nil {
		return port.PRInfo{}, err
	}
	var doc struct {
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"`
		IsDraft    bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return port.PRInfo{}, errcode.Wrap(errcode.Unknown, "gh pr view printed something that is not the expected JSON", err)
	}
	info := port.PRInfo{HeadOID: doc.HeadRefOid, IsDraft: doc.IsDraft}
	switch strings.ToUpper(doc.State) {
	case "OPEN":
		info.State = port.PRStateOpen
	case "CLOSED":
		info.State = port.PRStateClosed
	case "MERGED":
		info.State = port.PRStateMerged
	default:
		return port.PRInfo{}, errcode.New(errcode.Unknown, fmt.Sprintf("gh pr view reported the unknown state %q", doc.State))
	}
	if !validOID(info.HeadOID) {
		return port.PRInfo{}, errcode.New(errcode.Unknown, "gh pr view reported no usable head commit")
	}
	return info, nil
}

// validOID accepts a hexadecimal commit id (SHA-1 or SHA-256 length).
func validOID(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		isHex := r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
		if !isHex {
			return false
		}
	}
	return true
}
