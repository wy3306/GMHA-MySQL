package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	collectdomain "gmha/internal/collect"
)

// MachineInfoRepository 是机器信息的 SQLite 仓储实现。
type MachineInfoRepository struct {
	db *DB
}

func NewMachineInfoRepository(db *DB) *MachineInfoRepository {
	return &MachineInfoRepository{db: db}
}

func (r *MachineInfoRepository) Migrate() error {
	_, err := r.db.Exec(`
		create table if not exists machine_info (
			machine_id text primary key,
			ips_json text not null default '[]',
			interfaces_json text not null default '[]',
			hostname text not null default '',
			cpu_cores integer not null default 0,
			memory_gb integer not null default 0,
			arch text not null default '',
			glibc_version text not null default '',
			os text not null default '',
			disk_free_gb integer not null default 0,
			selinux text not null default '',
			firewall text not null default '',
			swap_enabled integer not null default 0,
			ntp_enabled integer not null default 0,
			time_offset_ms integer not null default 0,
			updated_at text not null
		);
	`)
	return err
}

func (r *MachineInfoRepository) Save(ctx context.Context, item collectdomain.MachineInfo) error {
	ipsJSON, _ := json.Marshal(item.IPs)
	ifacesJSON, _ := json.Marshal(item.Interfaces)
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		insert into machine_info (
			machine_id, ips_json, interfaces_json, hostname, cpu_cores, memory_gb, arch, glibc_version,
			os, disk_free_gb, selinux, firewall, swap_enabled, ntp_enabled, time_offset_ms, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(machine_id) do update set
			ips_json = excluded.ips_json,
			interfaces_json = excluded.interfaces_json,
			hostname = excluded.hostname,
			cpu_cores = excluded.cpu_cores,
			memory_gb = excluded.memory_gb,
			arch = excluded.arch,
			glibc_version = excluded.glibc_version,
			os = excluded.os,
			disk_free_gb = excluded.disk_free_gb,
			selinux = excluded.selinux,
			firewall = excluded.firewall,
			swap_enabled = excluded.swap_enabled,
			ntp_enabled = excluded.ntp_enabled,
			time_offset_ms = excluded.time_offset_ms,
			updated_at = excluded.updated_at
	`, item.MachineID, string(ipsJSON), string(ifacesJSON), item.Hostname, item.CPUCores, item.MemoryGB, item.Arch, item.GlibcVersion, item.OS, item.DiskFreeGB, item.SELinux, item.Firewall, boolInt(item.SwapEnabled), boolInt(item.NTPEnabled), item.TimeOffsetMS, item.UpdatedAt.UTC().Format(time.RFC3339))
	return err
}

func (r *MachineInfoRepository) Get(ctx context.Context, machineID string) (collectdomain.MachineInfo, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		select machine_id, ips_json, interfaces_json, hostname, cpu_cores, memory_gb, arch, glibc_version,
			os, disk_free_gb, selinux, firewall, swap_enabled, ntp_enabled, time_offset_ms, updated_at
		from machine_info where machine_id = ?
	`, machineID)
	var item collectdomain.MachineInfo
	var ipsJSON, ifacesJSON, updatedAt string
	var swapEnabled, ntpEnabled int
	if err := row.Scan(&item.MachineID, &ipsJSON, &ifacesJSON, &item.Hostname, &item.CPUCores, &item.MemoryGB, &item.Arch, &item.GlibcVersion, &item.OS, &item.DiskFreeGB, &item.SELinux, &item.Firewall, &swapEnabled, &ntpEnabled, &item.TimeOffsetMS, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return collectdomain.MachineInfo{}, false, nil
		}
		return collectdomain.MachineInfo{}, false, err
	}
	_ = json.Unmarshal([]byte(ipsJSON), &item.IPs)
	if err := json.Unmarshal([]byte(ifacesJSON), &item.Interfaces); err != nil || hasEmptyInterfaces(item.Interfaces) {
		var legacy []string
		if legacyErr := json.Unmarshal([]byte(ifacesJSON), &legacy); legacyErr == nil {
			item.Interfaces = make([]collectdomain.NetworkInterface, 0, len(legacy))
			for _, name := range legacy {
				item.Interfaces = append(item.Interfaces, collectdomain.NetworkInterface{
					Name: name,
					IPs:  []string{},
				})
			}
		}
	}
	item.SwapEnabled = swapEnabled == 1
	item.NTPEnabled = ntpEnabled == 1
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, true, nil
}

func (r *MachineInfoRepository) DeleteByMachineID(ctx context.Context, machineID string) error {
	_, err := r.db.ExecContext(ctx, `delete from machine_info where machine_id = ?`, machineID)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func hasEmptyInterfaces(items []collectdomain.NetworkInterface) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Name != "" {
			return false
		}
	}
	return true
}
