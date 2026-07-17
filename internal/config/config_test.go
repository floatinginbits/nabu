package config

import (
	"testing"
)

// A valid 32-byte auth secret, required by every case that expects success.
const testSecret = "0123456789abcdef0123456789abcdef"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults with required vars set",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "NABU_AUTH_SECRET": testSecret},
			want: Config{Port: 8080, DatabaseURL: "postgres://localhost/nabu", AuthSecret: testSecret, CookieSecure: true},
		},
		{
			name: "explicit port",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "NABU_AUTH_SECRET": testSecret, "PORT": "9090"},
			want: Config{Port: 9090, DatabaseURL: "postgres://localhost/nabu", AuthSecret: testSecret, CookieSecure: true},
		},
		{
			name:    "missing DATABASE_URL",
			env:     map[string]string{"NABU_AUTH_SECRET": testSecret},
			wantErr: true,
		},
		{
			name:    "non-numeric port",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "NABU_AUTH_SECRET": testSecret, "PORT": "abc"},
			wantErr: true,
		},
		{
			name:    "port out of range",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "NABU_AUTH_SECRET": testSecret, "PORT": "70000"},
			wantErr: true,
		},
		{
			name: "initial admin pair",
			env: map[string]string{
				"DATABASE_URL":                "postgres://localhost/nabu",
				"NABU_AUTH_SECRET":            testSecret,
				"NABU_INITIAL_ADMIN_EMAIL":    "admin@example.com",
				"NABU_INITIAL_ADMIN_PASSWORD": "s3cret-pw",
			},
			want: Config{
				Port:                 8080,
				DatabaseURL:          "postgres://localhost/nabu",
				InitialAdminEmail:    "admin@example.com",
				InitialAdminPassword: "s3cret-pw",
				AuthSecret:           testSecret,
				CookieSecure:         true,
			},
		},
		{
			name: "initial admin email without password",
			env: map[string]string{
				"DATABASE_URL":             "postgres://localhost/nabu",
				"NABU_AUTH_SECRET":         testSecret,
				"NABU_INITIAL_ADMIN_EMAIL": "admin@example.com",
			},
			wantErr: true,
		},
		{
			name: "initial admin password without email",
			env: map[string]string{
				"DATABASE_URL":                "postgres://localhost/nabu",
				"NABU_AUTH_SECRET":            testSecret,
				"NABU_INITIAL_ADMIN_PASSWORD": "s3cret-pw",
			},
			wantErr: true,
		},
		{
			name:    "missing auth secret",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu"},
			wantErr: true,
		},
		{
			name:    "auth secret too short",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/nabu", "NABU_AUTH_SECRET": "short"},
			wantErr: true,
		},
		{
			name: "cookie secure disabled",
			env: map[string]string{
				"DATABASE_URL":       "postgres://localhost/nabu",
				"NABU_AUTH_SECRET":   testSecret,
				"NABU_COOKIE_SECURE": "false",
			},
			want: Config{Port: 8080, DatabaseURL: "postgres://localhost/nabu", AuthSecret: testSecret, CookieSecure: false},
		},
		{
			name: "invalid cookie secure",
			env: map[string]string{
				"DATABASE_URL":       "postgres://localhost/nabu",
				"NABU_AUTH_SECRET":   testSecret,
				"NABU_COOKIE_SECURE": "maybe",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv with "" first isolates the test from vars set in the
			// developer's shell.
			for _, k := range []string{
				"PORT", "DATABASE_URL", "NABU_INITIAL_ADMIN_EMAIL",
				"NABU_INITIAL_ADMIN_PASSWORD", "NABU_AUTH_SECRET", "NABU_COOKIE_SECURE",
			} {
				t.Setenv(k, "")
			}
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
