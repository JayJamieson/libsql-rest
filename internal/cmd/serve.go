package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/JayJamieson/libsql-rest/internal/db"
	"github.com/JayJamieson/libsql-rest/internal/schema"
	"github.com/JayJamieson/libsql-rest/internal/server"
	"github.com/JayJamieson/libsql-rest/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "start a libsql-rest server",
	Long:  `start a libsql-rest server exposing your tables as a REST API`,
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := loadConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}

	sqlDB, err := db.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	// Verify connectivity early so misconfiguration fails fast rather than on
	// the first request.
	pingCtx, cancelPing := context.WithTimeout(cmd.Context(), 5*time.Second)
	defer cancelPing()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return err
	}

	introspector := schema.NewSQLIntrospector(sqlDB)
	dataStore := store.NewSQLStore(sqlDB, introspector, store.Options{
		MaxPageSize: cfg.MaxPageSize,
		Allow:       cfg.IsTableAllowed,
	})

	var middlewares []server.Middleware
	if cfg.Auth.Enabled {
		opts := auth.JWTOptions{
			Algorithm:           cfg.Auth.Algorithm,
			Issuer:              cfg.Auth.Issuer,
			Audience:            []string{cfg.Auth.Audience},
			CredentialsOptional: cfg.Auth.Optional,
		}
		if cfg.Auth.IsRSA() {
			pemBytes, err := os.ReadFile(cfg.Auth.PublicKeyPath)
			if err != nil {
				return fmt.Errorf("reading auth public key: %w", err)
			}
			pub, err := auth.ParseRSAPublicKey(pemBytes)
			if err != nil {
				return err
			}
			opts.RSAPublicKey = pub
		} else {
			opts.HMACSecret = []byte(cfg.Auth.Secret)
		}

		authMW, err := auth.NewJWTMiddleware(opts)
		if err != nil {
			return err
		}
		middlewares = append(middlewares, server.Middleware(authMW))
		slog.Info("jwt authentication enabled", "algorithm", cfg.Auth.Algorithm, "issuer", cfg.Auth.Issuer, "optional", cfg.Auth.Optional)
	}

	srv := server.New(server.Config{
		Host:        cfg.Server.Host,
		Port:        cfg.Server.Port,
		AuthEnabled: cfg.Auth.Enabled,
	}, dataStore, middlewares...)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	select {
	case err := <-errCh:
		return err
	case <-done:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// loadConfig assembles the application config from viper (flags, config file,
// and environment), falling back to defaults.
func loadConfig() config.Config {
	cfg := config.Default()

	if v := viper.GetString("server.host"); v != "" {
		cfg.Server.Host = v
	}
	if v := viper.GetInt("server.port"); v != 0 {
		cfg.Server.Port = v
	}
	if v := viper.GetString("db.driver"); v != "" {
		cfg.DB.Driver = config.Driver(v)
	}
	if v := viper.GetString("db.uri"); v != "" {
		cfg.DB.URI = v
	}
	if v := viper.GetString("db.token"); v != "" {
		cfg.DB.Token = v
	}
	if v := viper.GetInt("server.max_page_size"); v != 0 {
		cfg.MaxPageSize = v
	}
	if v := viper.GetStringSlice("allow_tables"); len(v) != 0 {
		cfg.AllowTables = v
	}

	cfg.Auth.Enabled = viper.GetBool("auth.enabled")
	cfg.Auth.Algorithm = viper.GetString("auth.algorithm")
	cfg.Auth.Secret = viper.GetString("auth.secret")
	cfg.Auth.PublicKeyPath = viper.GetString("auth.public_key_path")
	cfg.Auth.Issuer = viper.GetString("auth.issuer")
	cfg.Auth.Audience = viper.GetString("auth.audience")
	cfg.Auth.Optional = viper.GetBool("auth.optional")
	return cfg
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().IntP("port", "p", 8080, "port to start the server on")
	serveCmd.Flags().String("host", "", "host/interface to bind (empty binds all interfaces)")
	serveCmd.Flags().String("driver", string(config.DriverSQLite), "database driver: sqlite or libsql")
	serveCmd.Flags().String("uri", "", "database connection URI")
	serveCmd.Flags().String("token", "", "libsql auth token")
	serveCmd.Flags().Bool("auth", false, "enable JWT authentication")
	serveCmd.Flags().String("auth-algorithm", "", "JWT signing algorithm: HS256 (default) or RS256")
	serveCmd.Flags().String("auth-secret", "", "HMAC secret used to verify HS* JWTs")
	serveCmd.Flags().String("auth-public-key", "", "path to PEM RSA public key used to verify RS* JWTs")

	must(viper.BindPFlag("server.host", serveCmd.Flags().Lookup("host")))
	must(viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port")))
	must(viper.BindPFlag("db.driver", serveCmd.Flags().Lookup("driver")))
	must(viper.BindPFlag("db.uri", serveCmd.Flags().Lookup("uri")))
	must(viper.BindPFlag("db.token", serveCmd.Flags().Lookup("token")))
	must(viper.BindPFlag("auth.enabled", serveCmd.Flags().Lookup("auth")))
	must(viper.BindPFlag("auth.algorithm", serveCmd.Flags().Lookup("auth-algorithm")))
	must(viper.BindPFlag("auth.secret", serveCmd.Flags().Lookup("auth-secret")))
	must(viper.BindPFlag("auth.public_key_path", serveCmd.Flags().Lookup("auth-public-key")))
}

func must(err error) {
	if err != nil {
		slog.Error("failed to bind flag", "err", err)
		os.Exit(1)
	}
}
