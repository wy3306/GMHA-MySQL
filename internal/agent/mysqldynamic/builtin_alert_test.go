package mysqldynamic

import "testing"

func TestNormalizeReplicaThreadRunningProducesAlertableBoolean(t *testing.T) {
	tests := []struct {
		name   string
		status map[string]string
		value  string
		want   bool
	}{
		{name: "standalone is healthy", status: map[string]string{}, want: true},
		{name: "replica yes", status: map[string]string{"Replica_IO_Running": "Yes"}, value: "Yes", want: true},
		{name: "replica on", status: map[string]string{"Replica_IO_Running": "ON"}, value: "ON", want: true},
		{name: "replica numeric", status: map[string]string{"Replica_IO_Running": "1"}, value: "1", want: true},
		{name: "replica stopped", status: map[string]string{"Replica_IO_Running": "No"}, value: "No", want: false},
		{name: "replica unknown", status: map[string]string{"Replica_IO_Running": "Connecting"}, value: "Connecting", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeReplicaThreadRunning(tt.status, tt.value); got != tt.want {
				t.Fatalf("normalizeReplicaThreadRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoolFromONOFFSupportsSlowQueryLogValues(t *testing.T) {
	for _, value := range []string{"ON", "on", "1", "true", "YES"} {
		if !boolFromONOFF(value) {
			t.Fatalf("expected %q to normalize to true", value)
		}
	}
	for _, value := range []string{"OFF", "0", "false", "NO"} {
		if boolFromONOFF(value) {
			t.Fatalf("expected %q to normalize to false", value)
		}
	}
}
