package repository

import "testing"

func TestSQLOperationExtractsFirstStatementKeyword(t *testing.T) {
	tests := map[string]string{
		"SELECT id FROM runs":                    "SELECT",
		"\n\tinsert into messages values ($1)":   "INSERT",
		"UPDATE runs SET status = $1":            "UPDATE",
		"DELETE FROM project_members":            "DELETE",
		"BEGIN":                                  "BEGIN",
		"":                                       "UNKNOWN",
		"WITH updated AS (SELECT 1) SELECT *":    "WITH",
		"CREATE TABLE example (id text primary)": "CREATE",
	}
	for input, want := range tests {
		if got := sqlOperation(input); got != want {
			t.Fatalf("sqlOperation(%q) = %q, want %q", input, got, want)
		}
	}
}
