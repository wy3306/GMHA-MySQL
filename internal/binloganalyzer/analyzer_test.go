package binloganalyzer

import (
	"testing"
	"time"
)

func TestValidateConfigRejectsUnsafeRangeAndStartFile(t *testing.T) {
	base := Config{
		Host: "127.0.0.1", Port: 3306, User: "mha",
		StartTime: time.Now().Add(-time.Hour), EndTime: time.Now(),
		BigTxnMode: BigTransactionRows, BigTxnRowsThreshold: 1000,
	}
	if err := ValidateConfig(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	invalidFile := base
	invalidFile.StartFile = "mysql-bin.000001; DROP TABLE users"
	if err := ValidateConfig(invalidFile); err == nil {
		t.Fatal("unsafe start file should be rejected")
	}
	tooWide := base
	tooWide.StartTime = tooWide.EndTime.Add(-8 * 24 * time.Hour)
	if err := ValidateConfig(tooWide); err == nil {
		t.Fatal("range over seven days should be rejected")
	}
}

func TestClassifiesDMLDDLAndExtractsObject(t *testing.T) {
	if kind, ok := classifyDDL(" ALTER TABLE `orders`.`items` ADD COLUMN note text "); !ok || kind != "ALTER" {
		t.Fatalf("unexpected DDL classification: %q %v", kind, ok)
	}
	if object := ddlObject("ALTER TABLE `orders`.`items` ADD COLUMN note text"); object != "items" {
		t.Fatalf("unexpected DDL object %q", object)
	}
	if _, ok := classifyDDL("SELECT * FROM items"); ok {
		t.Fatal("SELECT must not be classified as DDL")
	}
}

func TestBucketSizingAndIndexStayBounded(t *testing.T) {
	start := time.Date(2026, 7, 23, 9, 0, 0, 0, time.Local)
	end := start.Add(90 * time.Minute)
	size := chooseBucketSize(end.Sub(start))
	buckets := makeBuckets(start, end, size)
	if size != time.Minute || len(buckets) != 90 {
		t.Fatalf("unexpected buckets: size=%s count=%d", size, len(buckets))
	}
	if index := bucketIndex(start, end, size, len(buckets)); index != len(buckets)-1 {
		t.Fatalf("end time should clamp to final bucket, got %d", index)
	}
}

func TestCommitTimestampPreservesMicrosecondPrecision(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	got := commitTimestamp(1_721_701_234_567_890, location)
	if got.UnixMicro() != 1_721_701_234_567_890 {
		t.Fatalf("unexpected commit timestamp: %s", got)
	}
	if got.Location() != location {
		t.Fatalf("unexpected timestamp location: %s", got.Location())
	}
}
