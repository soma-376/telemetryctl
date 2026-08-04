package config

import "os"

type Config struct {
	Port         string
	PostgresDSN  string
	InvitePepper string
	Distribution string
}

func Load() Config {
	return Config{
		Port:         env("PORT", "8088"),
		PostgresDSN:  env("ENROLLMENT_PG_DSN", "postgres://enrollment:enrollment@localhost:55433/enrollment?sslmode=disable"),
		InvitePepper: env("ENROLLMENT_INVITE_PEPPER", "dev-only-invite-pepper"),
		Distribution: env("PULSEMETRY_DIST_DIR", "dist"),
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
