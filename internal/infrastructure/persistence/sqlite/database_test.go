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
