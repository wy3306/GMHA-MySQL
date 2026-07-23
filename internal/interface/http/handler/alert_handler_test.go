package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	alertdomain "gmha/internal/domain/alert"
)

func TestWriteAlertMapsValidationNotFoundAndInternalErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "validation", err: alertdomain.Invalid("invalid"), want: http.StatusBadRequest},
		{name: "not found", err: alertdomain.ErrNotFound, want: http.StatusNotFound},
		{name: "conflict", err: alertdomain.ErrConflict, want: http.StatusConflict},
		{name: "internal", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeAlert(recorder, nil, tt.err)
			if recorder.Code != tt.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}
