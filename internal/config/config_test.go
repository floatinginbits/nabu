package config

import (
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults with required vars set",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/nabu"},
			want: Config{Port: 8080, DatabaseURL: "postgres://localhost/nabu"},
		},
		{
			name: "explicit port",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "PORT": "9090"},
			want: Config{Port: 9090, DatabaseURL: "postgres://localhost/nabu"},
		},
		{
			name:    "missing DATABASE_URL",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "non-numeric port",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "PORT": "abc"},
			wantErr: true,
		},
		{
			name:    "port out of range",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "PORT": "70000"},
			wantErr: true,
		},
		{
			name: "initial admin pair",
			env: map[string]string{
				"DATABASE_URL":                "postgres://localhost/nabu",
				"NABU_INITIAL_ADMIN_EMAIL":    "admin@example.com",
				"NABU_INITIAL_ADMIN_PASSWORD": "s3cret-pw",
			},
			want: Config{
				Port:                 8080,
				DatabaseURL:          "postgres://localhost/nabu",
				InitialAdminEmail:    "admin@example.com",
				InitialAdminPassword: "s3cret-pw",
			},
		},
		{
			name: "initial admin email without password",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/nabu",
				"NABU_INITIAL_ADMIN_EMAIL": "admin@example.com",
			},
			wantErr: true,
		},
		{
			name: "initial admin password without email",
			env: map[string]string{
				"DATABASE_URL":                "postgres://localhost/nabu",
				"NABU_INITIAL_ADMIN_PASSWORD": "s3cret-pw",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv with "" first isolates the test from vars set in the
			// developer's shell.
			t.Setenv("PORT", "")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("NABU_INITIAL_ADMIN_EMAIL", "")
			t.Setenv("NABU_INITIAL_ADMIN_PASSWORD", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if *got != tt.want {
				t.Errorf("Load() = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
