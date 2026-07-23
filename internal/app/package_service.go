package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mysqlapp "gmha/internal/mysql"
)

const defaultPackageRoot = "software"

var packageCategories = []string{"gmha-manager", "gmha-agent", "mysql", "percona-toolkit", "mysql-router", "xtrabackup", "binlog2sql", "mycat", "proxysql", "sysbench", "other"}

const packageIndexName = ".gmha-package-index.json"

// PackageItem 是安装包管理页面展示的文件元数据。
type PackageItem struct {
	Category    string `json:"category"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Size        int64  `json:"size"`
	Format      string `json:"format"`
	Arch        string `json:"arch"`
	SHA256      string `json:"sha256"`
	SourceURL   string `json:"source_url,omitempty"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

// PackageCatalogItem 是经过项目维护方确认的软件源，可在管理页面一键入库。
type PackageCatalogItem struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Arch        string `json:"arch"`
	SourceURL   string `json:"source_url"`
	Description string `json:"description"`
}

// PackageBundleProfile 描述一个经过兼容性校验的 MySQL 与工具组合。
// 前端只提交组合 ID，实际下载地址始终由 Manager 的可信目录决定。
type PackageBundleProfile struct {
	ID                    string   `json:"id"`
	Label                 string   `json:"label"`
	MySQLVersion          string   `json:"mysql_version"`
	Arch                  string   `json:"arch"`
	Default               bool     `json:"default"`
	MySQLCatalogID        string   `json:"mysql_catalog_id"`
	RecommendedCatalogIDs []string `json:"recommended_catalog_ids"`
	OptionalCatalogIDs    []string `json:"optional_catalog_ids,omitempty"`
	CompatibilityNote     string   `json:"compatibility_note"`
}

type PackageBundleResultItem struct {
	CatalogID string       `json:"catalog_id"`
	Status    string       `json:"status"`
	Item      *PackageItem `json:"item,omitempty"`
	Error     string       `json:"error,omitempty"`
}

type PackageBundleResult struct {
	BundleID string                    `json:"bundle_id"`
	Complete bool                      `json:"complete"`
	Results  []PackageBundleResultItem `json:"results"`
}

// PackageSettings 描述当前安装包存放位置及支持的分类、架构。
type PackageSettings struct {
	StoragePath            string                 `json:"storage_path"`
	DefaultStoragePath     string                 `json:"default_storage_path"`
	Categories             []string               `json:"categories"`
	SupportedArchitectures []string               `json:"supported_architectures"`
	Catalog                []PackageCatalogItem   `json:"catalog"`
	Bundles                []PackageBundleProfile `json:"bundles"`
}

type packageMetadata struct {
	Arch        string `json:"arch,omitempty"`
	Version     string `json:"version,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	Description string `json:"description,omitempty"`
}

type packageIndex struct {
	Items map[string]packageMetadata `json:"items"`
}

type packageStoreConfig struct {
	StoragePath string `json:"storage_path"`
}

// PackageService 管理 Manager 本地的软件安装包，文件按类别保存到独立目录。
type PackageService struct {
	mu            sync.RWMutex
	storagePath   string
	settingsPath  string
	mysqlSelector *mysqlapp.PackageSelector
	httpClient    *http.Client
}

// NewPackageService 初始化默认 software 目录，并恢复上次保存的自定义路径。
func NewPackageService(settingsPath string, mysqlSelector *mysqlapp.PackageSelector) (*PackageService, error) {
	s := &PackageService{
		settingsPath:  settingsPath,
		mysqlSelector: mysqlSelector,
		httpClient:    &http.Client{Timeout: 45 * time.Minute},
	}
	path := defaultPackageRoot
	if content, err := os.ReadFile(settingsPath); err == nil {
		var cfg packageStoreConfig
		if json.Unmarshal(content, &cfg) == nil && strings.TrimSpace(cfg.StoragePath) != "" {
			path = cfg.StoragePath
		}
	}
	if err := s.SetStoragePath(path); err != nil {
		return nil, err
	}
	return s, nil
}

// Settings 返回当前目录与支持范围。
func (s *PackageService) Settings() PackageSettings {
	s.mu.RLock()
	path := s.storagePath
	s.mu.RUnlock()
	defaultPath, _ := filepath.Abs(defaultPackageRoot)
	return PackageSettings{
		StoragePath:            path,
		DefaultStoragePath:     defaultPath,
		Categories:             append([]string(nil), packageCategories...),
		SupportedArchitectures: []string{"x86_64", "aarch64", "noarch", "未识别"},
		Catalog:                officialPackageCatalog(),
		Bundles:                officialPackageBundles(),
	}
}

func officialPackageCatalog() []PackageCatalogItem {
	return []PackageCatalogItem{
		{ID: "mysql-5.7.44-x86_64", Category: "mysql", Name: "mysql-5.7.44-linux-glibc2.12-x86_64.tar.gz", Version: "5.7.44", Arch: "x86_64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-5.7/mysql-5.7.44-linux-glibc2.12-x86_64.tar.gz", Description: "MySQL Community Server 5.7.44 Linux Generic（5.7 最终版）"},
		{ID: "mysql-8.0.46-x86_64", Category: "mysql", Name: "mysql-8.0.46-linux-glibc2.28-x86_64.tar.xz", Version: "8.0.46", Arch: "x86_64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-8.0.46-linux-glibc2.28-x86_64.tar.xz", Description: "MySQL Community Server 8.0.46 Linux Generic（8.0 当前维护版）"},
		{ID: "mysql-8.0.46-aarch64", Category: "mysql", Name: "mysql-8.0.46-linux-glibc2.28-aarch64.tar.xz", Version: "8.0.46", Arch: "aarch64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-8.0/mysql-8.0.46-linux-glibc2.28-aarch64.tar.xz", Description: "MySQL Community Server 8.0.46 Linux Generic ARM64"},
		{ID: "mysql-8.4.10-x86_64", Category: "mysql", Name: "mysql-8.4.10-linux-glibc2.28-x86_64.tar.xz", Version: "8.4.10", Arch: "x86_64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-8.4/mysql-8.4.10-linux-glibc2.28-x86_64.tar.xz", Description: "MySQL Community Server 8.4.10 LTS Linux Generic"},
		{ID: "mysql-8.4.10-aarch64", Category: "mysql", Name: "mysql-8.4.10-linux-glibc2.28-aarch64.tar.xz", Version: "8.4.10", Arch: "aarch64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-8.4/mysql-8.4.10-linux-glibc2.28-aarch64.tar.xz", Description: "MySQL Community Server 8.4.10 LTS Linux Generic ARM64"},
		{ID: "mysql-9.7.1-x86_64", Category: "mysql", Name: "mysql-9.7.1-linux-glibc2.28-x86_64.tar.xz", Version: "9.7.1", Arch: "x86_64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-9.7/mysql-9.7.1-linux-glibc2.28-x86_64.tar.xz", Description: "MySQL Community Server 9.7.1 LTS Linux Generic"},
		{ID: "mysql-9.7.1-aarch64", Category: "mysql", Name: "mysql-9.7.1-linux-glibc2.28-aarch64.tar.xz", Version: "9.7.1", Arch: "aarch64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-9.7/mysql-9.7.1-linux-glibc2.28-aarch64.tar.xz", Description: "MySQL Community Server 9.7.1 LTS Linux Generic ARM64"},
		{ID: "mysql-router-9.7.1-x86_64", Category: "mysql-router", Name: "mysql-router-9.7.1-linux-glibc2.28-x86_64.tar.xz", Version: "9.7.1", Arch: "x86_64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-Router/mysql-router-9.7.1-linux-glibc2.28-x86_64.tar.xz", Description: "Oracle MySQL Router 9.7.1 Linux Generic"},
		{ID: "mysql-router-9.7.1-aarch64", Category: "mysql-router", Name: "mysql-router-9.7.1-linux-glibc2.28-aarch64.tar.xz", Version: "9.7.1", Arch: "aarch64", SourceURL: "https://dev.mysql.com/get/Downloads/MySQL-Router/mysql-router-9.7.1-linux-glibc2.28-aarch64.tar.xz", Description: "Oracle MySQL Router 9.7.1 Linux Generic"},
		{ID: "binlog2sql-5a8e65c", Category: "binlog2sql", Name: "binlog2sql-5a8e65c-noarch.tar.gz", Version: "5a8e65c", Arch: "noarch", SourceURL: "https://github.com/danfengcao/binlog2sql/archive/5a8e65c432e74950b48b7ead28f424ec931b755d.tar.gz", Description: "MySQL binlog 解析与回滚 SQL 生成工具"},
		{ID: "mycat-1.6", Category: "mycat", Name: "Mycat-server-1.6-RELEASE-linux-noarch.tar.gz", Version: "1.6", Arch: "noarch", SourceURL: "https://raw.githubusercontent.com/MyCATApache/Mycat-download/master/1.6-RELEASE/Mycat-server-1.6-RELEASE-20161028204710-linux.tar.gz", Description: "Mycat Server 1.6 Linux 发行包"},
		{ID: "mycat2-1.22-source", Category: "mycat", Name: "Mycat2-v1.22-2022-6-25-source-noarch.tar.gz", Version: "1.22", Arch: "noarch", SourceURL: "https://github.com/MyCATApache/Mycat2/archive/refs/tags/v1.22-2022-6-25.tar.gz", Description: "Mycat2 v1.22 官方源码包（需 Java 8/Maven 构建）"},
		{ID: "proxysql-3.0.9-debian12-x86_64", Category: "proxysql", Name: "proxysql_3.0.9-debian12_amd64.deb", Version: "3.0.9", Arch: "x86_64", SourceURL: "https://github.com/sysown/proxysql/releases/download/v3.0.9/proxysql_3.0.9-debian12_amd64.deb", Description: "ProxySQL 3.0.9 Debian 12"},
		{ID: "proxysql-3.0.9-debian12-aarch64", Category: "proxysql", Name: "proxysql_3.0.9-debian12_arm64.deb", Version: "3.0.9", Arch: "aarch64", SourceURL: "https://github.com/sysown/proxysql/releases/download/v3.0.9/proxysql_3.0.9-debian12_arm64.deb", Description: "ProxySQL 3.0.9 Debian 12"},
		{ID: "sysbench-1.0.20-source", Category: "sysbench", Name: "sysbench-1.0.20-source-noarch.tar.gz", Version: "1.0.20", Arch: "noarch", SourceURL: "https://github.com/akopytov/sysbench/archive/refs/tags/1.0.20.tar.gz", Description: "Sysbench 1.0.20 官方源码包"},
		{ID: "percona-toolkit-3.7.1-source", Category: "percona-toolkit", Name: "percona-toolkit-3.7.1-noarch.tar.gz", Version: "3.7.1", Arch: "noarch", SourceURL: "https://github.com/percona/percona-toolkit/archive/refs/tags/v3.7.1.tar.gz", Description: "Percona Toolkit 3.7.1 源码包；离线部署前需按 README 的 PT 离线包章节合并目标发行版依赖"},
		{ID: "xtrabackup-8.0.35-36-x86_64", Category: "xtrabackup", Name: "percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz", Version: "8.0.35-36", Arch: "x86_64", SourceURL: "https://downloads.percona.com/downloads/Percona-XtraBackup-8.0/Percona-XtraBackup-8.0.35-36/binary/tarball/percona-xtrabackup-8.0.35-36-Linux-x86_64.glibc2.28-minimal.tar.gz", Description: "Percona XtraBackup 8.0 最终版，兼容 MySQL 8.0.34 及以上"},
		{ID: "xtrabackup-2.4.29-x86_64", Category: "xtrabackup", Name: "percona-xtrabackup-2.4.29-Linux-x86_64.glibc2.17.tar.gz", Version: "2.4.29", Arch: "x86_64", SourceURL: "https://downloads.percona.com/downloads/Percona-XtraBackup-2.4/Percona-XtraBackup-2.4.29/binary/tarball/percona-xtrabackup-2.4.29-Linux-x86_64.glibc2.17.tar.gz", Description: "Percona XtraBackup 2.4 最终完整包，兼容 MySQL 5.7（要求 glibc 2.17+）"},
		{ID: "xtrabackup-8.4.0-6-x86_64", Category: "xtrabackup", Name: "percona-xtrabackup-8.4.0-6-Linux-x86_64.glibc2.28-minimal.tar.gz", Version: "8.4.0-6", Arch: "x86_64", SourceURL: "https://downloads.percona.com/downloads/Percona-XtraBackup-8.4/Percona-XtraBackup-8.4.0-6/binary/tarball/percona-xtrabackup-8.4.0-6-Linux-x86_64.glibc2.28-minimal.tar.gz", Description: "Percona XtraBackup 8.4，匹配 MySQL 8.4 LTS"},
		{ID: "xtrabackup-9.7.1-rc1-x86_64", Category: "xtrabackup", Name: "percona-xtrabackup-9.7.1-rc1-Linux-x86_64.glibc2.28.tar.gz", Version: "9.7.1-rc1", Arch: "x86_64", SourceURL: "https://downloads.percona.com/downloads/Percona-XtraBackup-9.7/Percona-XtraBackup-9.7.1-rc1/binary/tarball/percona-xtrabackup-9.7.1-rc1-Linux-x86_64.glibc2.28.tar.gz", Description: "Percona XtraBackup 9.7.1 RC1，专用于 MySQL 9.7（上游预发布版）"},
	}
}

func officialPackageBundles() []PackageBundleProfile {
	commonX86 := []string{"mysql-router-9.7.1-x86_64", "sysbench-1.0.20-source"}
	commonARM := []string{"mysql-router-9.7.1-aarch64", "sysbench-1.0.20-source"}
	return []PackageBundleProfile{
		{
			ID: "mysql-5.7.44-x86_64", Label: "MySQL 5.7.44 · x86_64", MySQLVersion: "5.7.44", Arch: "x86_64",
			MySQLCatalogID:        "mysql-5.7.44-x86_64",
			RecommendedCatalogIDs: []string{"sysbench-1.0.20-source", "xtrabackup-2.4.29-x86_64"},
			OptionalCatalogIDs:    []string{"proxysql-3.0.9-debian12-x86_64", "binlog2sql-5a8e65c", "mycat-1.6"},
			CompatibilityNote:     "MySQL 5.7 最终稳定组合：XtraBackup 必须使用 2.4；Clone 与 MySQL 8 动态权限不可用；Toolkit 使用 Manager 离线包。",
		},
		{
			ID: "mysql-8.0.46-x86_64", Label: "MySQL 8.0.46 · x86_64", MySQLVersion: "8.0.46", Arch: "x86_64", Default: true,
			MySQLCatalogID:        "mysql-8.0.46-x86_64",
			RecommendedCatalogIDs: append(append([]string{}, commonX86...), "xtrabackup-8.0.35-36-x86_64"),
			OptionalCatalogIDs:    []string{"proxysql-3.0.9-debian12-x86_64", "binlog2sql-5a8e65c", "mycat2-1.22-source"},
			CompatibilityNote:     "默认稳定组合：Router 9.7 支持仍受支持的 MySQL Server；XtraBackup 8.0.35-36 支持 MySQL 8.0.34 及以上；Toolkit 请按目标 Linux 发行版另行制作离线依赖包。",
		},
		{
			ID: "mysql-8.4.10-x86_64", Label: "MySQL 8.4.10 LTS · x86_64", MySQLVersion: "8.4.10", Arch: "x86_64",
			MySQLCatalogID:        "mysql-8.4.10-x86_64",
			RecommendedCatalogIDs: append(append([]string{}, commonX86...), "xtrabackup-8.4.0-6-x86_64"),
			OptionalCatalogIDs:    []string{"proxysql-3.0.9-debian12-x86_64", "mycat2-1.22-source"},
			CompatibilityNote:     "LTS 组合：MySQL Router 9.7 与 XtraBackup 8.4 适配 MySQL 8.4；Toolkit 请按目标 Linux 发行版另行制作离线依赖包。",
		},
		{
			ID: "mysql-9.7.1-x86_64", Label: "MySQL 9.7.1 LTS · x86_64", MySQLVersion: "9.7.1", Arch: "x86_64",
			MySQLCatalogID:        "mysql-9.7.1-x86_64",
			RecommendedCatalogIDs: append(append([]string{}, commonX86...), "xtrabackup-9.7.1-rc1-x86_64"),
			OptionalCatalogIDs:    []string{"proxysql-3.0.9-debian12-x86_64", "mycat2-1.22-source"},
			CompatibilityNote:     "9.7 LTS 组合：Router 使用当前版本；Toolkit 可离线安装但上游尚未声明 9.7 支持，变更类 PT 命令需先预演；物理备份只能使用 XtraBackup 9.7，当前上游发布为 RC1，生产启用前需完成恢复演练。",
		},
		{
			ID: "mysql-8.0.46-aarch64", Label: "MySQL 8.0.46 · aarch64", MySQLVersion: "8.0.46", Arch: "aarch64",
			MySQLCatalogID:        "mysql-8.0.46-aarch64",
			RecommendedCatalogIDs: commonARM,
			CompatibilityNote:     "ARM64 官方通用二进制组合；XtraBackup 需另行上传与目标版本、glibc 和 aarch64 匹配的制品。",
		},
		{
			ID: "mysql-8.4.10-aarch64", Label: "MySQL 8.4.10 LTS · aarch64", MySQLVersion: "8.4.10", Arch: "aarch64",
			MySQLCatalogID:        "mysql-8.4.10-aarch64",
			RecommendedCatalogIDs: commonARM,
			CompatibilityNote:     "ARM64 8.4 LTS 官方通用二进制组合；XtraBackup 需另行上传匹配的 aarch64 制品。",
		},
		{
			ID: "mysql-9.7.1-aarch64", Label: "MySQL 9.7.1 LTS · aarch64", MySQLVersion: "9.7.1", Arch: "aarch64",
			MySQLCatalogID:        "mysql-9.7.1-aarch64",
			RecommendedCatalogIDs: commonARM,
			CompatibilityNote:     "ARM64 9.7 LTS 官方通用二进制组合；Percona 尚未在目录提供对应 aarch64 9.7 制品，物理备份需上传经过验证的同架构包。",
		},
	}
}

// FetchCatalogPackage 下载软件目录中经过确认的官方条目。
func (s *PackageService) FetchCatalogPackage(ctx context.Context, catalogID string) (PackageItem, error) {
	catalogID = strings.TrimSpace(catalogID)
	for _, item := range officialPackageCatalog() {
		if item.ID != catalogID {
			continue
		}
		return s.FetchFromURLWithMetadata(ctx, item.Category, item.Arch, item.SourceURL, item.Name, item.Version, item.Description)
	}
	return PackageItem{}, errors.New("unknown package catalog item")
}

// FetchPackageBundle 从官网顺序下载一个兼容组合；已存在的软件包会安全跳过。
func (s *PackageService) FetchPackageBundle(ctx context.Context, bundleID string) (PackageBundleResult, error) {
	var selected *PackageBundleProfile
	for _, profile := range officialPackageBundles() {
		if profile.ID == strings.TrimSpace(bundleID) {
			copy := profile
			selected = &copy
			break
		}
	}
	if selected == nil {
		return PackageBundleResult{}, errors.New("unknown package bundle")
	}
	catalogByID := make(map[string]PackageCatalogItem)
	for _, item := range officialPackageCatalog() {
		catalogByID[item.ID] = item
	}
	ids := append([]string{selected.MySQLCatalogID}, selected.RecommendedCatalogIDs...)
	result := PackageBundleResult{BundleID: selected.ID, Complete: true, Results: make([]PackageBundleResultItem, 0, len(ids))}
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		catalogItem, ok := catalogByID[id]
		if !ok {
			result.Complete = false
			result.Results = append(result.Results, PackageBundleResultItem{CatalogID: id, Status: "failed", Error: "catalog item is missing"})
			continue
		}
		if _, err := s.Open(catalogItem.Category, catalogItem.Name); err == nil {
			result.Results = append(result.Results, PackageBundleResultItem{CatalogID: id, Status: "already_installed"})
			continue
		}
		item, err := s.FetchCatalogPackage(ctx, id)
		if err != nil {
			result.Complete = false
			result.Results = append(result.Results, PackageBundleResultItem{CatalogID: id, Status: "failed", Error: err.Error()})
			continue
		}
		result.Results = append(result.Results, PackageBundleResultItem{CatalogID: id, Status: "downloaded", Item: &item})
	}
	return result, nil
}

// SetStoragePath 指定新的根目录，并即时同步 MySQL 自动选择器。
func (s *PackageService) SetStoragePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("storage_path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := ensurePackageDirectories(abs); err != nil {
		return err
	}
	s.mu.Lock()
	s.storagePath = abs
	s.mu.Unlock()
	if s.mysqlSelector != nil {
		s.mysqlSelector.SetSoftwareDir(filepath.Join(abs, "mysql"))
	}
	return s.saveSettings()
}

func (s *PackageService) saveSettings() error {
	s.mu.RLock()
	path := s.storagePath
	settingsPath := s.settingsPath
	s.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(packageStoreConfig{StoragePath: path}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, content, 0o600)
}

// List 返回经过分类、关键字过滤后的安装包。
func (s *PackageService) List(category, keyword string) ([]PackageItem, error) {
	category = strings.TrimSpace(category)
	if category != "" && !validPackageCategory(category) {
		return nil, errors.New("unsupported package category")
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	s.mu.Lock()
	root := s.storagePath
	defer s.mu.Unlock()
	index, err := loadPackageIndex(root)
	if err != nil {
		return nil, err
	}
	indexChanged := false
	categories := packageCategories
	if category != "" {
		categories = []string{category}
	}
	items := make([]PackageItem, 0)
	for _, kind := range categories {
		entries, err := os.ReadDir(filepath.Join(root, kind))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if keyword != "" && !strings.Contains(strings.ToLower(entry.Name()), keyword) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			item := newPackageItem(kind, entry.Name(), info)
			key := packageIndexKey(kind, entry.Name())
			metadata := index.Items[key]
			if catalogMetadata, ok := packageCatalogMetadata(kind, entry.Name()); ok {
				before := metadata
				if metadata.Arch == "" {
					metadata.Arch = catalogMetadata.Arch
				}
				if metadata.Version == "" {
					metadata.Version = catalogMetadata.Version
				}
				if metadata.SourceURL == "" {
					metadata.SourceURL = catalogMetadata.SourceURL
				}
				if metadata.Description == "" {
					metadata.Description = catalogMetadata.Description
				}
				indexChanged = indexChanged || metadata != before
			}
			if validArchitecture(metadata.Arch) {
				item.Arch = metadata.Arch
			}
			item.Version, item.SourceURL, item.Description = metadata.Version, metadata.SourceURL, metadata.Description
			if item.Version == "" {
				item.Version = detectPackageVersion(entry.Name())
				metadata.Version = item.Version
				indexChanged = true
			}
			item.SHA256 = metadata.SHA256
			metadata.Arch = item.Arch
			index.Items[key] = metadata
			items = append(items, item)
		}
	}
	if indexChanged {
		if err := savePackageIndex(root, index); err != nil {
			return nil, err
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].Name < items[j].Name
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items, nil
}

// ResolvePerconaToolkitPackage selects a local Toolkit archive for the target
// architecture. A noarch source archive is preferred because the core pt-*
// commands are Perl programs and can be deployed to both x86_64 and aarch64.
func (s *PackageService) ResolvePerconaToolkitPackage(arch, osName string) (string, error) {
	items, err := s.List("percona-toolkit", "")
	if err != nil {
		return "", err
	}
	arch = normalizePackageArch(arch)
	bestName, bestScore := "", -1
	for _, item := range items {
		if item.Format != "tar.gz" && item.Format != "tgz" {
			continue
		}
		score := perconaToolkitPackageScore(item, arch, osName)
		if score > bestScore {
			bestName, bestScore = item.Name, score
		}
	}
	if bestName != "" {
		return bestName, nil
	}
	return "", fmt.Errorf("no local Percona Toolkit package matches architecture %s", arch)
}

func perconaToolkitPackageScore(item PackageItem, targetArch, osName string) int {
	score := -1
	switch normalizePackageArch(item.Arch) {
	case targetArch:
		score = 100
	case "noarch":
		score = 10
	default:
		return -1
	}
	name := strings.ToLower(item.Name)
	targetFamily := linuxPackageFamily(osName)
	familyMarkers := map[string][]string{
		"debian": {"ubuntu", "debian"},
		"rhel":   {"rhel", "centos", "rocky", "alma"},
		"suse":   {"opensuse", "sles", "suse"},
		"alpine": {"alpine"},
		"arch":   {"archlinux"},
	}
	for family, markers := range familyMarkers {
		for _, marker := range markers {
			if !strings.Contains(name, marker) {
				continue
			}
			if family == targetFamily {
				score += 100
			} else {
				score -= 100
			}
			break
		}
	}
	if strings.Contains(name, "offline") {
		score += 5
	}
	return score
}

func linuxPackageFamily(osName string) string {
	name := strings.ToLower(osName)
	switch {
	case strings.Contains(name, "ubuntu"), strings.Contains(name, "debian"):
		return "debian"
	case strings.Contains(name, "red hat"), strings.Contains(name, "rhel"), strings.Contains(name, "centos"), strings.Contains(name, "rocky"), strings.Contains(name, "alma"):
		return "rhel"
	case strings.Contains(name, "suse"), strings.Contains(name, "sles"):
		return "suse"
	case strings.Contains(name, "alpine"):
		return "alpine"
	case strings.Contains(name, "arch linux"):
		return "arch"
	default:
		return ""
	}
}

// ResolveXtraBackupPackage selects a Manager-hosted XtraBackup binary archive
// whose release series matches the MySQL server and whose architecture/glibc
// requirements are compatible with the target host.
func (s *PackageService) ResolveXtraBackupPackage(mysqlVersion, arch, glibcVersion string) (string, error) {
	items, err := s.List("xtrabackup", "")
	if err != nil {
		return "", err
	}
	capabilities, err := mysqlapp.CapabilitiesForVersion(mysqlVersion)
	if err != nil {
		return "", err
	}
	series := capabilities.XtraBackupSeries
	arch = normalizePackageArch(arch)
	targetGlibc := parsePackageGlibc(glibcVersion)
	candidates := make([]PackageItem, 0)
	for _, item := range items {
		if normalizePackageArch(item.Arch) != arch || !strings.HasPrefix(item.Version, series+".") {
			continue
		}
		if !xtraBackupPackageSupportsServer(item.Version, mysqlVersion) {
			continue
		}
		if item.Format != "tar.gz" && item.Format != "tgz" {
			continue
		}
		requiredGlibc := parsePackageGlibc(item.Name)
		if targetGlibc > 0 && requiredGlibc > targetGlibc {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no local XtraBackup %s package matches architecture %s and glibc %s", series, arch, glibcVersion)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return parsePackageGlibc(candidates[i].Name) > parsePackageGlibc(candidates[j].Name)
		}
		return comparePackageRelease(candidates[i].Version, candidates[j].Version) > 0
	})
	return candidates[0].Name, nil
}

// Package release identifiers include values such as 8.0.35-36 and
// 9.7.1-rc1. Lexical ordering would incorrectly rank 8.0.9 above 8.0.35, so
// select offline tools by numeric components and prefer GA over prereleases
// when the numeric components are otherwise equal.
func comparePackageRelease(left, right string) int {
	numbers := func(value string) []int {
		lower := strings.ToLower(value)
		for _, marker := range []string{"rc", "beta", "alpha"} {
			if index := strings.Index(lower, marker); index >= 0 {
				value = value[:index]
				break
			}
		}
		parts := regexp.MustCompile(`[0-9]+`).FindAllString(value, -1)
		out := make([]int, 0, len(parts))
		for _, part := range parts {
			number, _ := strconv.Atoi(part)
			out = append(out, number)
		}
		return out
	}
	a, b := numbers(left), numbers(right)
	for index := 0; index < len(a) || index < len(b); index++ {
		var av, bv int
		if index < len(a) {
			av = a[index]
		}
		if index < len(b) {
			bv = b[index]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	isPrerelease := func(value string) bool {
		value = strings.ToLower(value)
		return strings.Contains(value, "rc") || strings.Contains(value, "beta") || strings.Contains(value, "alpha")
	}
	if isPrerelease(left) != isPrerelease(right) {
		if isPrerelease(left) {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

// Current XtraBackup 8.0.34+ releases intentionally dropped direct support
// for MySQL 8.0.0-8.0.33. Those servers require a matching historical PXB
// build, which the user can keep in the offline repository. Innovation and
// LTS releases are already isolated by their exact x.y series above.
func xtraBackupPackageSupportsServer(xtraBackupVersion, mysqlVersion string) bool {
	serverParts := strings.Split(strings.TrimSpace(mysqlVersion), ".")
	if len(serverParts) < 3 || serverParts[0] != "8" || serverParts[1] != "0" {
		return true
	}
	serverPatch, err := strconv.Atoi(serverParts[2])
	if err != nil || serverPatch >= 34 {
		return true
	}
	base := strings.Split(strings.TrimSpace(xtraBackupVersion), "-")[0]
	parts := strings.Split(base, ".")
	if len(parts) < 3 {
		return false
	}
	xtraBackupPatch, err := strconv.Atoi(parts[2])
	return err == nil && xtraBackupPatch < 34
}

func parsePackageGlibc(value string) int {
	if index := strings.LastIndex(strings.ToLower(value), "glibc"); index >= 0 {
		value = value[index+len("glibc"):]
	}
	match := regexp.MustCompile(`^\s*([0-9]+)\.([0-9]+)`).FindStringSubmatch(value)
	if len(match) != 3 {
		return 0
	}
	var major, minor int
	_, _ = fmt.Sscanf(match[1]+"."+match[2], "%d.%d", &major, &minor)
	return major*1000 + minor
}

func normalizePackageArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

// SaveUpload 将上传内容流式写入分类目录，不允许路径穿越或覆盖已有文件。
func (s *PackageService) SaveUpload(category, arch, name string, content io.Reader) (PackageItem, error) {
	return s.SaveUploadWithMetadata(category, arch, name, "", "", content)
}

// SaveUploadWithMetadata 保存手工上传的软件包，并优先使用用户确认的版本和说明。
// Manager 与 Agent 升级必须具备明确版本，避免文件成功入库后无法做升级/降级判断。
func (s *PackageService) SaveUploadWithMetadata(category, arch, name, version, description string, content io.Reader) (PackageItem, error) {
	category = strings.TrimSpace(category)
	version = strings.TrimSpace(version)
	if version == "" {
		version = detectPackageVersion(name)
	}
	if (category == "gmha-manager" || category == "gmha-agent") && version == "" {
		return PackageItem{}, errors.New("Manager/Agent package version is required; enter Vx.y.z or include it in the file name")
	}
	return s.save(category, name, content, packageMetadata{Arch: arch, Version: version, Description: strings.TrimSpace(description)})
}

// FetchFromURL 下载 HTTP(S) 安装包到分类目录，适用于官方或第三方发布地址。
func (s *PackageService) FetchFromURL(ctx context.Context, category, arch, sourceURL, name string) (PackageItem, error) {
	return s.FetchFromURLWithMetadata(ctx, category, arch, sourceURL, name, "", "")
}

// FetchFromURLWithMetadata 从 HTTP(S) 软件源下载文件并持久化版本、来源和说明。
func (s *PackageService) FetchFromURLWithMetadata(ctx context.Context, category, arch, sourceURL, name, version, description string) (PackageItem, error) {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return PackageItem{}, errors.New("only valid http/https package URLs are supported")
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(parsed.Path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return PackageItem{}, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return PackageItem{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return PackageItem{}, fmt.Errorf("package download failed: %s", resp.Status)
	}
	if version == "" {
		version = detectPackageVersion(name)
	}
	return s.save(category, name, io.LimitReader(resp.Body, 8<<30), packageMetadata{
		Arch: arch, Version: version, SourceURL: parsed.String(), Description: strings.TrimSpace(description),
	})
}

func (s *PackageService) save(category, name string, content io.Reader, metadata packageMetadata) (PackageItem, error) {
	category = strings.TrimSpace(category)
	if !validPackageCategory(category) {
		return PackageItem{}, errors.New("unsupported package category")
	}
	name, err := safePackageName(name)
	if err != nil {
		return PackageItem{}, err
	}
	s.mu.RLock()
	dir := filepath.Join(s.storagePath, category)
	s.mu.RUnlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PackageItem{}, err
	}
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return PackageItem{}, errors.New("package file already exists; delete it before uploading a replacement")
		}
		return PackageItem{}, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), content)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return PackageItem{}, copyErr
		}
		return PackageItem{}, closeErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return PackageItem{}, err
	}
	item := newPackageItem(category, name, info)
	if validArchitecture(metadata.Arch) {
		item.Arch = metadata.Arch
	}
	metadata.Arch = item.Arch
	metadata.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	if metadata.Version == "" {
		metadata.Version = detectPackageVersion(name)
	}
	item.Version, item.SHA256, item.SourceURL, item.Description = metadata.Version, metadata.SHA256, metadata.SourceURL, metadata.Description
	s.mu.Lock()
	index, indexErr := loadPackageIndex(s.storagePath)
	if indexErr == nil {
		index.Items[packageIndexKey(category, name)] = metadata
		indexErr = savePackageIndex(s.storagePath, index)
	}
	s.mu.Unlock()
	if indexErr != nil {
		_ = os.Remove(path)
		return PackageItem{}, indexErr
	}
	return item, nil
}

// Open 返回下载文件的安全路径。
func (s *PackageService) Open(category, name string) (string, error) {
	if !validPackageCategory(category) {
		return "", errors.New("unsupported package category")
	}
	name, err := safePackageName(name)
	if err != nil {
		return "", err
	}
	s.mu.RLock()
	path := filepath.Join(s.storagePath, category, name)
	s.mu.RUnlock()
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("package path is a directory")
	}
	return path, nil
}

// Delete 删除 Manager 本地的安装包，不会影响已经部署到机器上的软件。
func (s *PackageService) Delete(category, name string) error {
	path, err := s.Open(category, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := loadPackageIndex(s.storagePath)
	if err != nil {
		return err
	}
	delete(index.Items, packageIndexKey(category, filepath.Base(path)))
	return savePackageIndex(s.storagePath, index)
}

// Verify 重新计算指定文件的 SHA-256，并把结果写回软件索引。
func (s *PackageService) Verify(category, name string) (PackageItem, error) {
	path, err := s.Open(category, name)
	if err != nil {
		return PackageItem{}, err
	}
	checksum, err := fileSHA256(path)
	if err != nil {
		return PackageItem{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return PackageItem{}, err
	}
	s.mu.Lock()
	index, err := loadPackageIndex(s.storagePath)
	if err == nil {
		key := packageIndexKey(category, filepath.Base(path))
		metadata := index.Items[key]
		metadata.SHA256 = checksum
		if metadata.Version == "" {
			metadata.Version = detectPackageVersion(name)
		}
		if metadata.Arch == "" {
			metadata.Arch = detectPackageArch(name)
		}
		if catalogMetadata, ok := packageCatalogMetadata(category, filepath.Base(path)); ok {
			if metadata.Version == "" {
				metadata.Version = catalogMetadata.Version
			}
			if metadata.Arch == "" || metadata.Arch == "未识别" {
				metadata.Arch = catalogMetadata.Arch
			}
			metadata.SourceURL, metadata.Description = catalogMetadata.SourceURL, catalogMetadata.Description
		}
		index.Items[key] = metadata
		err = savePackageIndex(s.storagePath, index)
	}
	s.mu.Unlock()
	if err != nil {
		return PackageItem{}, err
	}
	item := newPackageItem(category, filepath.Base(path), info)
	item.Version, item.Arch, item.SHA256, item.SourceURL, item.Description = index.Items[packageIndexKey(category, item.Name)].Version, index.Items[packageIndexKey(category, item.Name)].Arch, checksum, index.Items[packageIndexKey(category, item.Name)].SourceURL, index.Items[packageIndexKey(category, item.Name)].Description
	return item, nil
}

func ensurePackageDirectories(root string) error {
	for _, category := range packageCategories {
		if err := os.MkdirAll(filepath.Join(root, category), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func validPackageCategory(category string) bool {
	for _, item := range packageCategories {
		if category == item {
			return true
		}
	}
	return false
}

func safePackageName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || filepath.Base(name) != name || strings.Contains(name, string(filepath.Separator)) {
		return "", errors.New("invalid package file name")
	}
	return name, nil
}

func newPackageItem(category, name string, info os.FileInfo) PackageItem {
	return PackageItem{Category: category, Name: name, Size: info.Size(), Format: packageFormat(name), Arch: detectPackageArch(name), UpdatedAt: info.ModTime().UTC().Format(time.RFC3339)}
}

func packageFormat(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.xz"):
		return "tar.xz"
	case strings.HasSuffix(lower, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tgz"):
		return "tgz"
	case strings.HasSuffix(lower, ".rpm"):
		return "rpm"
	case strings.HasSuffix(lower, ".deb"):
		return "deb"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".bin"):
		return "binary"
	default:
		return "other"
	}
}

func detectPackageArch(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "x86_64"), strings.Contains(lower, "amd64"):
		return "x86_64"
	case strings.Contains(lower, "aarch64"), strings.Contains(lower, "arm64"):
		return "aarch64"
	case strings.Contains(lower, "noarch"), strings.Contains(lower, "all.deb"):
		return "noarch"
	default:
		return "未识别"
	}
}

func validArchitecture(arch string) bool {
	return arch == "x86_64" || arch == "aarch64" || arch == "noarch" || arch == "未识别"
}

var packageVersionPattern = regexp.MustCompile(`(?i)(?:^|[-_v])([0-9]+(?:\.[0-9]+){1,3})`)

func detectPackageVersion(name string) string {
	match := packageVersionPattern.FindStringSubmatch(filepath.Base(name))
	if len(match) < 2 {
		return ""
	}
	return strings.Trim(match[1], "-_.")
}

func packageIndexKey(category, name string) string { return category + "/" + name }

func packageCatalogMetadata(category, name string) (packageMetadata, bool) {
	for _, item := range officialPackageCatalog() {
		if item.Category == category && item.Name == name {
			return packageMetadata{Arch: item.Arch, Version: item.Version, SourceURL: item.SourceURL, Description: item.Description}, true
		}
	}
	return packageMetadata{}, false
}

func loadPackageIndex(root string) (packageIndex, error) {
	index := packageIndex{Items: make(map[string]packageMetadata)}
	content, err := os.ReadFile(filepath.Join(root, packageIndexName))
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return packageIndex{}, err
	}
	if err := json.Unmarshal(content, &index); err != nil {
		return packageIndex{}, fmt.Errorf("read package index: %w", err)
	}
	if index.Items == nil {
		index.Items = make(map[string]packageMetadata)
	}
	return index, nil
}

func savePackageIndex(root string, index packageIndex) error {
	content, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(root, packageIndexName+".tmp")
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(root, packageIndexName))
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
