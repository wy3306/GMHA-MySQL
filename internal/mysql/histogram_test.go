package mysql

import (
	"strings"
	"testing"
)

func TestSupportsHistogramForVersionRejectsMySQL57(t *testing.T) {
	for _, version := range []string{"8.0", "8.0.11", "8.0.40-0ubuntu0.22.04.1", "8.4.10", "9.7.1"} {
		if !SupportsHistogramForVersion(version) {
			t.Fatalf("expected %s to support histogram management", version)
		}
	}
	for _, version := range []string{"5.7.44", "5.7.44-log", "10.11.6-MariaDB", "", "unknown"} {
		if SupportsHistogramForVersion(version) {
			t.Fatalf("expected %s to reject histogram management", version)
		}
	}
}

func TestHistogramIdentifiersAreQuotedAndValidated(t *testing.T) {
	schema, table, columns, err := histogramIdentifiers("order`db", "sales", []string{"region", "order"})
	if err != nil {
		t.Fatal(err)
	}
	if schema != "`order``db`" || table != "`sales`" || strings.Join(columns, ",") != "`region`,`order`" {
		t.Fatalf("unexpected quoted identifiers: %s %s %v", schema, table, columns)
	}
	if _, _, _, err := histogramIdentifiers("orders", "sales", []string{"region", "region"}); err == nil {
		t.Fatal("duplicate columns must be rejected")
	}
	if _, _, _, err := histogramIdentifiers("orders", "sales", nil); err == nil {
		t.Fatal("empty columns must be rejected")
	}
	if _, _, _, err := histogramIdentifiers("orders", "sales", []string{strings.Repeat("列", 65)}); err == nil {
		t.Fatal("identifiers longer than 64 characters must be rejected")
	}
}

func TestHistogramColumnEligibility(t *testing.T) {
	if eligible, _ := histogramColumnEligibility("varchar", false); !eligible {
		t.Fatal("ordinary varchar columns should be eligible")
	}
	for _, dataType := range []string{"JSON", "geometry", "POINT"} {
		if eligible, reason := histogramColumnEligibility(dataType, false); eligible || reason == "" {
			t.Fatalf("%s should be ineligible with a reason", dataType)
		}
	}
	if eligible, reason := histogramColumnEligibility("bigint", true); eligible || !strings.Contains(reason, "唯一索引") {
		t.Fatalf("single-column unique index should be ineligible: %v %q", eligible, reason)
	}
}
