// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"context"
	"testing"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	st := openAt(t, dbPath(t)).Settings()

	if v, ok, err := st.Get(ctx, "concurrency"); err != nil || ok || v != "" {
		t.Errorf("Get of an unset key = %q, %v, %v", v, ok, err)
	}
	if err := st.Set(ctx, "concurrency", "2"); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "concurrency", "4"); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "language", "tr"); err != nil {
		t.Fatal(err)
	}
	// An empty value is a value, not an unset key.
	if err := st.Set(ctx, "cli_path", ""); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := st.Get(ctx, "concurrency"); !ok || v != "4" {
		t.Errorf("Get after overwrite = %q, %v", v, ok)
	}
	if v, ok, _ := st.Get(ctx, "cli_path"); !ok || v != "" {
		t.Errorf("empty value = %q, %v; want set and empty", v, ok)
	}

	all, err := st.All(ctx)
	if err != nil || len(all) != 3 || all["language"] != "tr" || all["concurrency"] != "4" {
		t.Errorf("All = %v, %v", all, err)
	}

	if err := st.Delete(ctx, "language"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "language"); err != nil {
		t.Errorf("deleting an unset key: %v", err)
	}
	if _, ok, _ := st.Get(ctx, "language"); ok {
		t.Error("key still set after Delete")
	}
}
