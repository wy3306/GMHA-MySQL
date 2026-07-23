package mysql

import (
	"strings"
	"testing"
)

func TestNormalizeExplainStatementAllowsOneExplainableStatement(t *testing.T) {
	cases := []string{
		"SELECT * FROM orders WHERE id = 1;",
		"/* ticket-42 */ WITH recent AS (SELECT id FROM orders) SELECT * FROM recent",
		"UPDATE orders SET status = 'paid' WHERE id = 1",
		"SELECT ';' AS separator",
	}
	for _, input := range cases {
		result, err := NormalizeExplainStatement(input)
		if err != nil {
			t.Fatalf("NormalizeExplainStatement(%q) returned %v", input, err)
		}
		if strings.HasSuffix(result, ";") {
			t.Fatalf("trailing semicolon was not removed: %q", result)
		}
	}
}

func TestNormalizeExplainStatementRejectsUnsafeOrMultipleInputs(t *testing.T) {
	cases := []string{
		"",
		"EXPLAIN ANALYZE SELECT * FROM orders",
		"SELECT 1; SELECT 2",
		"DROP TABLE orders",
		"SHOW PROCESSLIST",
	}
	for _, input := range cases {
		if _, err := NormalizeExplainStatement(input); err == nil {
			t.Fatalf("NormalizeExplainStatement(%q) should fail", input)
		}
	}
}

func TestExplainIdentifierValidation(t *testing.T) {
	for _, value := range []string{"orders", "orders_2026", "业务库", "tenant$1"} {
		if !safeMySQLIdentifier(value) {
			t.Fatalf("expected safe database name %q", value)
		}
	}
	for _, value := range []string{"", "orders-prod", "orders` USE mysql", "orders.name"} {
		if safeMySQLIdentifier(value) {
			t.Fatalf("expected unsafe database name %q", value)
		}
	}
}
