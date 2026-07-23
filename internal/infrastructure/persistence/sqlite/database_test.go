package sqlite

import "testing"

func TestDialectSQL(t *testing.T) {
	tests := []struct {
		name, query, want string
		dialect           Dialect
	}{
		{"sqlite", "select * from tasks where id = ?", "select * from tasks where id = ?", DialectSQLite},
		{"postgres", "update tasks set status = ? where id = ?", "update tasks set status = $1 where id = $2", DialectPostgres},
		{"mysql upsert", "on conflict(id) do update set status=excluded.status", "on duplicate key update status=values(status)", DialectMySQL},
		{"mysql conflict clause", "insert into tasks(id) values (?) on conflict(id) do update set status=excluded.status", "insert into tasks(id) values (?) on duplicate key update status=values(status)", DialectMySQL},
		{"postgres identity", "id integer primary key autoincrement", "id bigserial primary key", DialectPostgres},
		{"mysql identity", "id integer primary key autoincrement", "id bigint auto_increment primary key", DialectMySQL},
		{"mysql 5.7 text primary key", "id text primary key", "id varchar(191) primary key", DialectMySQL},
		{"mysql 5.7 text default", "spec_json text not null default ''", "spec_json longtext not null", DialectMySQL},
		{"mysql 5.7 index syntax", "create unique index if not exists idx_machine on machines(ip)", "create unique index idx_machine on machines(ip)", DialectMySQL},
		{"mysql 5.7 integer cast", "select cast(json_extract(spec_json, '$.port') as integer) from tasks", "select cast(json_extract(spec_json, '$.port') as signed) from tasks", DialectMySQL},
		{"mysql current reserved identifier", "select last_value from alert_evaluation_state", "select `last_value` from alert_evaluation_state", DialectMySQL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (&DB{dialect: tt.dialect}).sql(tt.query); got != tt.want {
				t.Fatalf("sql() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBindPostgresDoesNotBindQuotedQuestionMark(t *testing.T) {
	got := bindPostgres("select '?' as literal, id from tasks where id = ?")
	want := "select '?' as literal, id from tasks where id = $1"
	if got != want {
		t.Fatalf("bindPostgres() = %q, want %q", got, want)
	}
}

func TestMySQL57MigrationTablesUseUTF8MB4(t *testing.T) {
	got := mysqlMigrationStatement("create table if not exists clusters (name varchar(191) primary key)")
	if got != "create table if not exists clusters (name varchar(191) primary key) default character set utf8mb4 collate utf8mb4_unicode_ci" {
		t.Fatalf("unexpected MySQL migration statement: %s", got)
	}
}
