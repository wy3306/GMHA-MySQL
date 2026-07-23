package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type packageRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn packageRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestPackageServiceListsPerconaToolkitPackages(t *testing.T) {
	root := t.TempDir()
	if err := ensurePackageDirectories(root); err != nil {
		t.Fatal(err)
	}
	name := "percona-toolkit-3.7.1-noarch.tar.gz"
	if err := os.WriteFile(filepath.Join(root, "percona-toolkit", name), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	if !slices.Contains(service.Settings().Categories, "percona-toolkit") {
		t.Fatal("package settings should advertise the percona-toolkit category")
	}
	items, err := service.List("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Category != "percona-toolkit" || items[0].Name != name || items[0].Arch != "noarch" {
		t.Fatalf("unexpected package list: %#v", items)
	}
}

func TestResolvePerconaToolkitPackagePrefersNoarch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "percona-toolkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"percona-toolkit-3.7.0-x86_64.tar.gz",
		"percona-toolkit-3.7.1-noarch.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolvePerconaToolkitPackage("aarch64", "Ubuntu 22.04")
	if err != nil {
		t.Fatal(err)
	}
	if name != "percona-toolkit-3.7.1-noarch.tar.gz" {
		t.Fatalf("selected package %q", name)
	}
}

func TestResolvePerconaToolkitPackageRejectsMissingArchitecture(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "percona-toolkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "percona-toolkit-3.7.1-x86_64.tar.gz"), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	if _, err := service.ResolvePerconaToolkitPackage("aarch64", "Ubuntu 22.04"); err == nil {
		t.Fatal("expected an architecture mismatch error")
	}
}

func TestResolvePerconaToolkitPackagePrefersNativeOfflineBundle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "percona-toolkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"percona-toolkit-3.7.1-noarch.tar.gz", "percona-toolkit-3.7.1-linux-offline-x86_64.tar.gz"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolvePerconaToolkitPackage("x86_64", "Rocky Linux 9.4")
	if err != nil {
		t.Fatal(err)
	}
	if name != "percona-toolkit-3.7.1-linux-offline-x86_64.tar.gz" {
		t.Fatalf("selected package %q", name)
	}
}

func TestResolvePerconaToolkitPackageMatchesLinuxFamily(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "percona-toolkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"percona-toolkit-3.7.1-ubuntu22-offline-x86_64.tar.gz",
		"percona-toolkit-3.7.1-rocky9-offline-x86_64.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolvePerconaToolkitPackage("x86_64", "Ubuntu 22.04.5 LTS")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "ubuntu22") {
		t.Fatalf("selected package %q", name)
	}
}

func TestResolveXtraBackupPackageMatchesSeriesArchitectureAndGlibc(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "xtrabackup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz",
		"percona-xtrabackup-8.0.34-29-Linux-x86_64.glibc2.17-minimal.tar.gz",
		"percona-xtrabackup-8.4.0-6-Linux-x86_64.glibc2.28-minimal.tar.gz",
		"percona-xtrabackup-8.0.35-36-Linux-aarch64.glibc2.28-minimal.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolveXtraBackupPackage("8.0.44", "amd64", "2.31")
	if err != nil {
		t.Fatal(err)
	}
	if name != "percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz" {
		t.Fatalf("selected package %q", name)
	}
	name, err = service.ResolveXtraBackupPackage("8.0.44", "x86_64", "2.17")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "8.0.34-29") {
		t.Fatalf("expected glibc 2.17 package, got %q", name)
	}
}

func TestResolveXtraBackupPackageMapsMySQL57To24(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "xtrabackup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"percona-xtrabackup-2.4.29-Linux-x86_64.glibc2.17-minimal.tar.gz",
		"percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolveXtraBackupPackage("5.7.44", "x86_64", "2.17")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "xtrabackup-2.4.29") {
		t.Fatalf("expected XtraBackup 2.4 for MySQL 5.7, got %q", name)
	}
}

func TestResolveXtraBackupPackageMatchesMySQL97Only(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "xtrabackup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"percona-xtrabackup-8.4.0-6-Linux-x86_64.glibc2.28-minimal.tar.gz",
		"percona-xtrabackup-9.7.1-rc1-Linux-x86_64.glibc2.28.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := &PackageService{storagePath: root}
	name, err := service.ResolveXtraBackupPackage("9.7.1", "x86_64", "2.31")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "xtrabackup-9.7.1-rc1") {
		t.Fatalf("expected XtraBackup 9.7 for MySQL 9.7, got %q", name)
	}
}

func TestResolveXtraBackupPackageRejectsIncompatibleTarget(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "xtrabackup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "percona-xtrabackup-8.4.0-6-Linux-x86_64.glibc2.28-minimal.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	if _, err := service.ResolveXtraBackupPackage("8.0.44", "x86_64", "2.31"); err == nil {
		t.Fatal("expected MySQL/XtraBackup series mismatch")
	}
	if _, err := service.ResolveXtraBackupPackage("8.4.10", "x86_64", "2.17"); err == nil {
		t.Fatal("expected glibc mismatch")
	}
}

func TestResolveXtraBackupPackageRequiresHistoricalBuildForEarlyMySQL80(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "xtrabackup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	current := "percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, current), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	if _, err := service.ResolveXtraBackupPackage("8.0.29", "x86_64", "2.31"); err == nil || !strings.Contains(err.Error(), "XtraBackup 8.0") {
		t.Fatalf("early MySQL 8.0 should reject current-only PXB repository, got %v", err)
	}
	historical := "percona-xtrabackup-8.0.32-25-Linux-x86_64.glibc2.28-minimal.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, historical), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := service.ResolveXtraBackupPackage("8.0.29", "x86_64", "2.31")
	if err != nil || name != historical {
		t.Fatalf("expected historical PXB for MySQL 8.0.29, got %q, %v", name, err)
	}
}

func TestComparePackageReleaseUsesNumericOrdering(t *testing.T) {
	if comparePackageRelease("8.0.35-36", "8.0.9-10") <= 0 {
		t.Fatal("8.0.35-36 must sort after 8.0.9-10")
	}
	if comparePackageRelease("9.7.1", "9.7.1-rc1") <= 0 {
		t.Fatal("GA release must sort after an otherwise equivalent RC")
	}
}

func TestPackageUploadPersistsArchitectureAndChecksum(t *testing.T) {
	root := t.TempDir()
	if err := ensurePackageDirectories(root); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	item, err := service.SaveUpload("binlog2sql", "noarch", "binlog2sql-1.0.0-noarch.tar.gz", strings.NewReader("package-content"))
	if err != nil {
		t.Fatal(err)
	}
	if item.Arch != "noarch" || item.Version != "1.0.0" || len(item.SHA256) != 64 {
		t.Fatalf("unexpected uploaded metadata: %#v", item)
	}

	reloaded := &PackageService{storagePath: root}
	items, err := reloaded.List("binlog2sql", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Arch != "noarch" || items[0].SHA256 != item.SHA256 {
		t.Fatalf("metadata was not persisted: %#v", items)
	}
}

func TestComponentPackageUploadRequiresVersionMetadata(t *testing.T) {
	root := t.TempDir()
	if err := ensurePackageDirectories(root); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	if _, err := service.SaveUploadWithMetadata("gmha-agent", "x86_64", "agent-current.bin", "", "", strings.NewReader("binary")); err == nil {
		t.Fatal("expected component upload without a detectable version to fail")
	}
	item, err := service.SaveUploadWithMetadata("gmha-agent", "x86_64", "agent-current.bin", "V1.2.3", "stable release", strings.NewReader("binary"))
	if err != nil {
		t.Fatal(err)
	}
	if item.Version != "V1.2.3" || item.Description != "stable release" {
		t.Fatalf("unexpected component metadata: %#v", item)
	}
}

func TestOfficialCatalogCoversMySQLTools(t *testing.T) {
	categories := map[string]bool{}
	for _, item := range officialPackageCatalog() {
		categories[item.Category] = true
		if item.SourceURL == "" || item.Name == "" || item.Version == "" {
			t.Fatalf("incomplete catalog entry: %#v", item)
		}
	}
	for _, category := range []string{"mysql", "mysql-router", "xtrabackup", "percona-toolkit", "binlog2sql", "mycat", "proxysql", "sysbench"} {
		if !categories[category] {
			t.Fatalf("official catalog is missing %s", category)
		}
	}
}

func TestDefaultPackageBundleUsesCurrentMySQL80X86AndResolvableCatalog(t *testing.T) {
	catalog := map[string]bool{}
	for _, item := range officialPackageCatalog() {
		catalog[item.ID] = true
	}
	defaults := 0
	for _, bundle := range officialPackageBundles() {
		if bundle.Default {
			defaults++
			if bundle.MySQLVersion != "8.0.46" || bundle.Arch != "x86_64" {
				t.Fatalf("unexpected default bundle: %#v", bundle)
			}
		}
		for _, id := range append([]string{bundle.MySQLCatalogID}, bundle.RecommendedCatalogIDs...) {
			if !catalog[id] {
				t.Fatalf("bundle %s references missing catalog item %s", bundle.ID, id)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly one default bundle, got %d", defaults)
	}
}

func TestFetchPackageBundleDownloadsRecommendedOfficialItems(t *testing.T) {
	root := t.TempDir()
	if err := ensurePackageDirectories(root); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{
		storagePath: root,
		httpClient: &http.Client{Transport: packageRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("official-package")), Header: make(http.Header), Request: req}, nil
		})},
	}
	result, err := service.FetchPackageBundle(context.Background(), "mysql-8.0.46-x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Results) != 4 {
		t.Fatalf("unexpected bundle result: %#v", result)
	}
	for _, item := range result.Results {
		if item.Status != "downloaded" || item.Item == nil || len(item.Item.SHA256) != 64 {
			t.Fatalf("unexpected downloaded item: %#v", item)
		}
	}
	result, err = service.FetchPackageBundle(context.Background(), "mysql-8.0.46-x86_64")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Results {
		if item.Status != "already_installed" {
			t.Fatalf("expected idempotent skip, got %#v", item)
		}
	}
}

func TestVerifyIndexesLegacyPackage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mycat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "Mycat-server-1.6-RELEASE-linux-noarch.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("legacy-package"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &PackageService{storagePath: root}
	item, err := service.Verify("mycat", name)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.SHA256) != 64 || item.Arch != "noarch" {
		t.Fatalf("unexpected verification result: %#v", item)
	}
}
