package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesGlossarySchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, table := range []string{
		"glossary_terms",
		"glossary_variants",
		"glossary_observations",
		"glossary_jobs",
	} {
		var name string
		err := db.QueryRowContext(ctx, `select name from sqlite_master where type = 'table' and name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
