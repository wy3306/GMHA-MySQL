package sqlite

import (
	"context"
	"database/sql"
	"testing"

	mysqlapp "gmha/internal/mysql"
	_ "modernc.org/sqlite"
)

func TestMySQLInstanceRepositoryPersistsVersionAndArchitecture(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/instances.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewMySQLInstanceRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	want := mysqlapp.Instance{
		MachineID: "machine-1", Port: 3306, ServerID: 1, MySQLUser: "mysql",
		DataDir: "/data/3306/data", BaseDir: "/usr/local/mysql",
		PackageName: "mysql-8.4.6-linux-glibc2.17-aarch64.tar.xz",
		Version:     "8.4.6", Architecture: "aarch64", Status: mysqlapp.StatusRunning,
	}
	if err := repo.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := repo.Get(context.Background(), want.MachineID, want.Port)
	if err != nil || !ok {
		t.Fatalf("Get() = %+v, %v, %v", got, ok, err)
	}
	if got.Version != want.Version || got.Architecture != want.Architecture {
		t.Fatalf("structured package metadata changed: got version=%q architecture=%q", got.Version, got.Architecture)
	}
}

func TestMySQLInstanceRepositoryBackfillsLegacyPackageMetadata(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/legacy-instances.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewMySQLInstanceRepository(NewDB(db, DialectSQLite))
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	legacy := mysqlapp.Instance{
		MachineID: "machine-legacy", Port: 3306, ServerID: 1, MySQLUser: "mysql",
		DataDir: "/data/3306/data", BaseDir: "/usr/local/mysql",
		PackageName: "mysql-8.0.35-linux-glibc2.17-x86_64.tar.xz", Status: mysqlapp.StatusRunning,
	}
	if err := repo.Save(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	got, ok, err := repo.Get(context.Background(), legacy.MachineID, legacy.Port)
	if err != nil || !ok {
		t.Fatalf("Get() = %+v, %v, %v", got, ok, err)
	}
	if got.Version != "8.0.35" || got.Architecture != "x86_64" {
		t.Fatalf("legacy metadata was not recovered: version=%q architecture=%q", got.Version, got.Architecture)
	}
}
