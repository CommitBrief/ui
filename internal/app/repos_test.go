// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/ui/internal/domain/repo"
	"github.com/CommitBrief/ui/internal/port"
)

// memStore is an in-memory port.RepoStore + port.TxRunner with the same
// uniqueness rule as the SQLite adapter (owner/name, case-insensitive).
type memStore struct {
	rows   map[int64]repo.Repository
	nextID int64
	// failCreateAt makes the n-th Create call (1-based) fail, to prove that a
	// transaction rolls back.
	failCreateAt int
	creates      int
}

func newMemStore() *memStore { return &memStore{rows: map[int64]repo.Repository{}} }

func key(owner, name string) string { return strings.ToLower(owner) + "/" + strings.ToLower(name) }

func (m *memStore) find(owner, name string) (repo.Repository, bool) {
	for _, r := range m.rows {
		if key(r.Owner, r.Name) == key(owner, name) {
			return r, true
		}
	}
	return repo.Repository{}, false
}

func (m *memStore) Create(_ context.Context, r repo.Repository) (repo.Repository, error) {
	m.creates++
	if m.failCreateAt == m.creates {
		return repo.Repository{}, errors.New("disk full")
	}
	if _, dup := m.find(r.Owner, r.Name); dup {
		return repo.Repository{}, port.ErrConflict
	}
	m.nextID++
	r.ID = m.nextID
	r.CreatedAt = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	m.rows[r.ID] = r
	return r, nil
}

func (m *memStore) Get(_ context.Context, id int64) (repo.Repository, error) {
	r, ok := m.rows[id]
	if !ok {
		return repo.Repository{}, port.ErrNotFound
	}
	return r, nil
}

func (m *memStore) GetByRemote(_ context.Context, owner, name string) (repo.Repository, error) {
	if r, ok := m.find(owner, name); ok {
		return r, nil
	}
	return repo.Repository{}, port.ErrNotFound
}

func (m *memStore) List(context.Context) ([]repo.Repository, error) {
	out := []repo.Repository{}
	for _, r := range m.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Owner, out[i].Name) < key(out[j].Owner, out[j].Name) })
	return out, nil
}

func (m *memStore) Update(_ context.Context, r repo.Repository) error {
	if _, ok := m.rows[r.ID]; !ok {
		return port.ErrNotFound
	}
	if other, dup := m.find(r.Owner, r.Name); dup && other.ID != r.ID {
		return port.ErrConflict
	}
	cur := m.rows[r.ID]
	cur.Owner, cur.Name, cur.LocalPath = r.Owner, r.Name, r.LocalPath // enabled is not written
	m.rows[r.ID] = cur
	return nil
}

func (m *memStore) SetEnabled(_ context.Context, id int64, enabled bool) error {
	r, ok := m.rows[id]
	if !ok {
		return port.ErrNotFound
	}
	r.Enabled = enabled
	m.rows[id] = r
	return nil
}

func (m *memStore) Delete(_ context.Context, id int64) error {
	if _, ok := m.rows[id]; !ok {
		return port.ErrNotFound
	}
	delete(m.rows, id)
	return nil
}

func (m *memStore) InTx(_ context.Context, fn func(port.Tx) error) error {
	snapshot := make(map[int64]repo.Repository, len(m.rows))
	for k, v := range m.rows {
		snapshot[k] = v
	}
	nextID := m.nextID
	if err := fn(memTx{m}); err != nil {
		m.rows, m.nextID = snapshot, nextID
		return err
	}
	return nil
}

type memTx struct{ m *memStore }

func (t memTx) Repos() port.RepoStore        { return t.m }
func (t memTx) Settings() port.SettingsStore { return nil }

// fakeInspector knows a fixed set of checkouts: path -> origin (empty origin
// means a checkout without a remote).
type fakeInspector struct {
	checkouts map[string]string
	origins   map[string]port.RemoteRef
	checked   []string
}

func (f *fakeInspector) CheckRoot(_ context.Context, path string) error {
	f.checked = append(f.checked, path)
	if _, ok := f.checkouts[path]; !ok {
		return &repo.Error{Code: repo.CodeLocalPathMissing, Field: "localPath"}
	}
	return nil
}

func (f *fakeInspector) Origin(ctx context.Context, path string) (port.RemoteRef, error) {
	if err := f.CheckRoot(ctx, path); err != nil {
		return port.RemoteRef{}, err
	}
	ref, ok := f.origins[path]
	if !ok {
		return port.RemoteRef{}, repo.New(repo.CodeNoRemote)
	}
	return ref, nil
}

const (
	pathUI  = "/work/ui"
	pathCLI = "/work/cli"
)

func newService(t *testing.T) (*Repos, *memStore, *fakeInspector) {
	t.Helper()
	st := newMemStore()
	in := &fakeInspector{
		checkouts: map[string]string{pathUI: "x", pathCLI: "x", "/work/noremote": "x"},
		origins: map[string]port.RemoteRef{
			pathUI:  {Host: "github.com", Owner: "CommitBrief", Name: "ui"},
			pathCLI: {Host: "github.com", Owner: "CommitBrief", Name: "commitbrief"},
		},
	}
	s := NewRepos(st, st, in)
	s.now = func() time.Time { return time.Date(2026, 11, 10, 15, 4, 5, 0, time.UTC) }
	return s, st, in
}

func wantCode(t *testing.T, err error, code repo.Code) *repo.Error {
	t.Helper()
	var e *repo.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
	return e
}

func TestAddValidatesAndStores(t *testing.T) {
	ctx := context.Background()
	s, st, in := newService(t)

	r, err := s.Add(ctx, RepoInput{Owner: " CommitBrief ", Name: "ui", LocalPath: pathUI + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 || r.Owner != "CommitBrief" || r.LocalPath != pathUI || !r.Enabled {
		t.Errorf("Add = %+v", r)
	}
	if len(in.checked) != 1 || in.checked[0] != pathUI {
		t.Errorf("the cleaned path must be checked, got %v", in.checked)
	}

	// Without a local path no file system check happens.
	in.checked = nil
	if _, err := s.Add(ctx, RepoInput{Owner: "a", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	if len(in.checked) != 0 {
		t.Errorf("unexpected path check: %v", in.checked)
	}

	bad := []struct {
		in   RepoInput
		code repo.Code
	}{
		{RepoInput{Owner: "", Name: "x"}, repo.CodeOwnerInvalid},
		{RepoInput{Owner: "a/b", Name: "x"}, repo.CodeOwnerInvalid},
		{RepoInput{Owner: "a", Name: "x y"}, repo.CodeNameInvalid},
		{RepoInput{Owner: "a", Name: "x", LocalPath: "relative"}, repo.CodeLocalPathInvalid},
		{RepoInput{Owner: "a", Name: "x", LocalPath: "/does/not/exist"}, repo.CodeLocalPathMissing},
	}
	for _, tc := range bad {
		before := len(st.rows)
		_, err := s.Add(ctx, tc.in)
		wantCode(t, err, tc.code)
		if len(st.rows) != before {
			t.Errorf("a rejected Add(%+v) stored a row", tc.in)
		}
	}

	_, err = s.Add(ctx, RepoInput{Owner: "commitbrief", Name: "UI"})
	wantCode(t, err, repo.CodeDuplicate)
}

func TestUpdateSetEnabledDeleteGet(t *testing.T) {
	ctx := context.Background()
	s, _, in := newService(t)
	a, _ := s.Add(ctx, RepoInput{Owner: "o", Name: "a", LocalPath: pathUI})
	b, _ := s.Add(ctx, RepoInput{Owner: "o", Name: "b"})

	off, err := s.SetEnabled(ctx, a.ID, false)
	if err != nil || off.Enabled {
		t.Fatalf("SetEnabled = %+v, %v", off, err)
	}

	// Update keeps the enabled flag and the creation time.
	in.checked = nil
	up, err := s.Update(ctx, a.ID, RepoInput{Owner: "o", Name: "renamed", LocalPath: pathUI})
	if err != nil {
		t.Fatal(err)
	}
	if up.Enabled || up.Name != "renamed" || !up.CreatedAt.Equal(a.CreatedAt) {
		t.Errorf("Update = %+v", up)
	}
	if len(in.checked) != 0 {
		t.Errorf("an unchanged local path must not be re-checked, got %v", in.checked)
	}

	// A changed path is checked.
	if _, err := s.Update(ctx, a.ID, RepoInput{Owner: "o", Name: "renamed", LocalPath: pathCLI}); err != nil {
		t.Fatal(err)
	}
	if len(in.checked) != 1 {
		t.Errorf("a changed path must be checked once, got %v", in.checked)
	}
	_, err = s.Update(ctx, a.ID, RepoInput{Owner: "o", Name: "renamed", LocalPath: "/gone"})
	wantCode(t, err, repo.CodeLocalPathMissing)

	_, err = s.Update(ctx, a.ID, RepoInput{Owner: "o", Name: "B"})
	wantCode(t, err, repo.CodeDuplicate)
	_, err = s.Update(ctx, 999, RepoInput{Owner: "o", Name: "x"})
	wantCode(t, err, repo.CodeNotFound)
	_, err = s.Update(ctx, a.ID, RepoInput{Owner: "o", Name: "bad name"})
	wantCode(t, err, repo.CodeNameInvalid)
	_, err = s.SetEnabled(ctx, 999, true)
	wantCode(t, err, repo.CodeNotFound)

	got, err := s.Get(ctx, b.ID)
	if err != nil || got.Name != "b" {
		t.Errorf("Get = %+v, %v", got, err)
	}
	_, err = s.Get(ctx, 999)
	wantCode(t, err, repo.CodeNotFound)

	if err := s.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Delete(ctx, b.ID), repo.CodeNotFound)
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("List = %+v", list)
	}
}

func TestSuggestFromLocalPath(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)

	got, err := s.SuggestFromLocalPath(ctx, pathUI)
	if err != nil {
		t.Fatal(err)
	}
	if got != (RemoteSuggestion{Host: "github.com", Owner: "CommitBrief", Name: "ui"}) {
		t.Errorf("suggestion = %+v", got)
	}

	if _, err := s.Add(ctx, RepoInput{Owner: "commitbrief", Name: "UI"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SuggestFromLocalPath(ctx, pathUI)
	if !got.AlreadyAdded {
		t.Errorf("AlreadyAdded = false for a repository in the list: %+v", got)
	}

	_, err = s.SuggestFromLocalPath(ctx, "/work/noremote")
	wantCode(t, err, repo.CodeNoRemote)
	_, err = s.SuggestFromLocalPath(ctx, "/nowhere")
	wantCode(t, err, repo.CodeLocalPathMissing)
	_, err = s.SuggestFromLocalPath(ctx, "relative")
	wantCode(t, err, repo.CodeLocalPathInvalid)
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, _, _ := newService(t)
	mustAdd := func(in RepoInput) repo.Repository {
		t.Helper()
		r, err := src.Add(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	mustAdd(RepoInput{Owner: "CommitBrief", Name: "ui", LocalPath: pathUI})
	off := mustAdd(RepoInput{Owner: "CommitBrief", Name: "commitbrief.com"})
	if _, err := src.SetEnabled(ctx, off.ID, false); err != nil {
		t.Fatal(err)
	}
	mustAdd(RepoInput{Owner: "muhammetsafak", Name: "notes"})

	doc, err := src.ExportJSON(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Schema     string
		Version    int
		ExportedAt string
	}
	if err := json.Unmarshal([]byte(doc), &head); err != nil || head.Schema != ExportSchema || head.Version != ExportVersion || head.ExportedAt != "2026-11-10T15:04:05Z" {
		t.Fatalf("export header = %+v, %v\n%s", head, err, doc)
	}

	// Import into a fresh machine that has the checkout of ui at the same
	// path (path kept) -- then again on one that does not (path dropped).
	dst, dstStore, _ := newService(t)
	res, err := dst.ImportJSON(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if res != (ImportResult{Created: 3}) {
		t.Errorf("result = %+v, want 3 created", res)
	}
	want, _ := src.List(ctx)
	got, _ := dst.List(ctx)
	if len(got) != len(want) {
		t.Fatalf("imported %d, want %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Owner != g.Owner || w.Name != g.Name || w.LocalPath != g.LocalPath || w.Enabled != g.Enabled {
			t.Errorf("row %d: got %+v, want %+v", i, g, w)
		}
	}

	// Importing the same file again changes nothing.
	res, err = dst.ImportJSON(ctx, doc)
	if err != nil || res != (ImportResult{Skipped: 3}) {
		t.Errorf("second import = %+v, %v; want 3 skipped", res, err)
	}
	if len(dstStore.rows) != 3 {
		t.Errorf("second import changed the list: %d rows", len(dstStore.rows))
	}

	// On a machine without the checkout the repository is imported, the path is not.
	other, _, otherInspect := newService(t)
	delete(otherInspect.checkouts, pathUI)
	res, err = other.ImportJSON(ctx, doc)
	if err != nil || res != (ImportResult{Created: 3, PathsDropped: 1}) {
		t.Errorf("import without the checkout = %+v, %v", res, err)
	}
	r, _ := other.store.GetByRemote(ctx, "commitbrief", "ui")
	if r.LocalPath != "" {
		t.Errorf("unusable path was kept: %q", r.LocalPath)
	}
}

func TestImportRejectsBadInputAtomically(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	if _, err := s.Add(ctx, RepoInput{Owner: "existing", Name: "one"}); err != nil {
		t.Fatal(err)
	}

	file := func(version int, entries string) string {
		return `{"schema":"` + ExportSchema + `","version":` + itoa(version) + `,"repositories":[` + entries + `]}`
	}
	tests := []struct {
		name  string
		data  string
		code  repo.Code
		entry int
	}{
		{"not json", `{`, repo.CodeImportInvalid, 0},
		{"wrong schema", `{"schema":"other","version":1,"repositories":[]}`, repo.CodeImportInvalid, 0},
		{"no version", `{"schema":"` + ExportSchema + `","repositories":[]}`, repo.CodeImportInvalid, 0},
		{"newer version", file(ExportVersion+1, ""), repo.CodeImportSchema, 0},
		{"bad owner in entry 2", file(1, `{"owner":"ok","name":"fine"},{"owner":"no good","name":"x"}`), repo.CodeOwnerInvalid, 2},
		{"bad name in entry 1", file(1, `{"owner":"ok","name":"../x"}`), repo.CodeNameInvalid, 1},
		{"too many entries", file(1, strings.TrimSuffix(strings.Repeat(`{"owner":"a","name":"b"},`, MaxImportEntries+1), ",")), repo.CodeImportTooLarge, 0},
		{"too large", strings.Repeat(" ", MaxImportBytes+1), repo.CodeImportTooLarge, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := len(st.rows)
			_, err := s.ImportJSON(ctx, tc.data)
			e := wantCode(t, err, tc.code)
			if e.Entry != tc.entry {
				t.Errorf("Entry = %d, want %d", e.Entry, tc.entry)
			}
			if len(st.rows) != before {
				t.Errorf("a rejected import wrote %d row(s)", len(st.rows)-before)
			}
		})
	}

	// A failure in the middle of the write rolls back the whole import.
	st.failCreateAt = st.creates + 2
	_, err := s.ImportJSON(ctx, file(1, `{"owner":"n","name":"one"},{"owner":"n","name":"two"},{"owner":"n","name":"three"}`))
	if err == nil || len(st.rows) != 1 {
		t.Errorf("mid-import failure = %v, rows = %d; want an error and the original single row", err, len(st.rows))
	}
}

func TestImportToleratesHandWrittenFiles(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	// No exportedAt, no enabled, unknown fields, repeated entry.
	data := `{"schema":"` + ExportSchema + `","version":1,"futureField":{"x":1},"repositories":[
		{"owner":"a","name":"b","color":"red"},
		{"owner":"A","name":"B"},
		{"owner":"a","name":"c","enabled":false}
	]}`
	res, err := s.ImportJSON(ctx, data)
	if err != nil || res != (ImportResult{Created: 2, Skipped: 1}) {
		t.Fatalf("import = %+v, %v", res, err)
	}
	b, _ := s.store.GetByRemote(ctx, "a", "b")
	c, _ := s.store.GetByRemote(ctx, "a", "c")
	if !b.Enabled || c.Enabled {
		t.Errorf("enabled defaults: b=%v c=%v", b.Enabled, c.Enabled)
	}
}

func TestExportEmptyList(t *testing.T) {
	s, _, _ := newService(t)
	doc, err := s.ExportJSON(context.Background())
	if err != nil || !strings.Contains(doc, `"repositories": []`) {
		t.Errorf("empty export = %q, %v", doc, err)
	}
	if res, err := s.ImportJSON(context.Background(), doc); err != nil || res != (ImportResult{}) {
		t.Errorf("importing an empty export = %+v, %v", res, err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// SetEnabled and Update return the row as stored, and neither undoes the other.
func TestUpdateKeepsEnabledSetMeanwhile(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	r, _ := s.Add(ctx, RepoInput{Owner: "o", Name: "a"})
	if _, err := s.SetEnabled(ctx, r.ID, false); err != nil {
		t.Fatal(err)
	}
	up, err := s.Update(ctx, r.ID, RepoInput{Owner: "o", Name: "b"})
	if err != nil || up.Enabled || up.Name != "b" {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	on, err := s.SetEnabled(ctx, r.ID, true)
	if err != nil || !on.Enabled || on.Name != "b" {
		t.Fatalf("SetEnabled = %+v, %v", on, err)
	}
	if st.rows[r.ID].Name != "b" || !st.rows[r.ID].Enabled {
		t.Errorf("stored row = %+v", st.rows[r.ID])
	}
}
