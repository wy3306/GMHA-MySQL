package handler

import (
	"net/http/httptest"
	"testing"
)

func TestDiagnosticHistoryQueryParsesSorting(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/sql-diagnostics/slow?sort_by=rows_examined&direction=asc", nil)
	query, err := diagnosticHistoryQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if query.SortBy != "rows_examined" || query.SortDirection != "asc" {
		t.Fatalf("unexpected sorting query: %+v", query)
	}
}

func TestDiagnosticHistoryQueryRejectsInvalidDirection(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/sql-diagnostics/top?direction=sideways", nil)
	if _, err := diagnosticHistoryQuery(request); err == nil {
		t.Fatal("expected invalid direction to be rejected")
	}
}
