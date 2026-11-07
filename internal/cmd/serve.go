package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/JayJamieson/libsql-rest/internal/db"
	"github.com/JayJamieson/libsql-rest/internal/schema"
	"github.com/JayJamieson/libsql-rest/internal/server"
	"github.com/JayJamieson/libsql-rest/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var configFlags = map[string]string{
	"server.host":          "host",
	"server.port":          "port",
	"server.max_page_size": "max-page-size",
	"allow_tables":         "allow-tables",
	"db.driver":            "driver",
	"db.uri":               "uri",
	"db.token":             "token",
	"auth.enabled":         "auth",
	"auth.algorithm":       "auth-algorithm",
	"auth.secret":          "auth-secret",
	"auth.public_key_path": "auth-public-key",
	"auth.issuer":          "auth-issuer",
	"auth.audience":        "auth-audience",
	"auth.optional":        "auth-optional",
}

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		Long: `Start the HTTP server.

Settings come from flags, then LIBSQL_REST_* environment variables, then the
config file, then the defaults shown below.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return err
			}
			return serve(cmd.Context(), cfg, cmd.Root().Version)
		},
	}

	d := config.Default()
	f := cmd.Flags()
	f.String("config", "", "config file (default $HOME/.libsql-rest.yaml)")
	f.String("host", d.Server.Host, "interface to bind; empty binds all interfaces")
	f.IntP("port", "p", d.Server.Port, "port to listen on")
	f.Int("max-page-size", d.MaxPageSize, "default and maximum rows per list request")
	f.StringSlice("allow-tables", nil, "tables and views to expose; empty exposes all")
	f.String("driver", string(d.DB.Driver), "database driver: sqlite or libsql")
	f.String("uri", d.DB.URI, "database URI")
	f.String("token", "", "libsql auth token")
	f.Bool("auth", false, "require a JWT bearer token on API requests")
	f.String("auth-algorithm", d.Auth.Algorithm, "JWT algorithm: HS256, HS384, HS512, RS256, RS384, or RS512")
	f.String("auth-secret", "", "HMAC secret for HS* algorithms")
	f.String("auth-public-key", "", "PEM file with the RSA public key for RS* algorithms")
	f.String("auth-issuer", d.Auth.Issuer, "required iss claim")
	f.String("auth-audience", d.Auth.Audience, "required aud claim")
	f.Bool("auth-optional", false, "let requests without a token through as anonymous")
	return cmd
}

func loadConfig(flags *pflag.FlagSet) (config.Config, error) {
	v := viper.New()
	for key, name := range configFlags {
		if err := v.BindPFlag(key, flags.Lookup(name)); err != nil {
			return config.Config{}, fmt.Errorf("binding --%s: %w", name, err)
		}
	}
	v.SetEnvPrefix("libsql_rest")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if file := flags.Lookup("config").Value.String(); file != "" {
		v.SetConfigFile(file)
	} else if home, err := os.UserHomeDir(); err == nil {
		v.AddConfigPath(home)
		v.SetConfigName(".libsql-rest")
		v.SetConfigType("yaml")
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return config.Config{}, fmt.Errorf("reading config file: %w", err)
		}
	} else {
		slog.Info("loaded config file", "path", v.ConfigFileUsed())
	}

	cfg := config.Config{
		Server: config.ServerConfig{
			Host: v.GetString("server.host"),
			Port: v.GetInt("server.port"),
		},
		DB: config.DBConfig{
			Driver: config.Driver(v.GetString("db.driver")),
			URI:    v.GetString("db.uri"),
			Token:  v.GetString("db.token"),
		},
		Auth: config.AuthConfig{
			Enabled:       v.GetBool("auth.enabled"),
			Algorithm:     v.GetString("auth.algorithm"),
			Secret:        v.GetString("auth.secret"),
			PublicKeyPath: v.GetString("auth.public_key_path"),
			Issuer:        v.GetString("auth.issuer"),
			Audience:      v.GetString("auth.audience"),
			Optional:      v.GetBool("auth.optional"),
		},
		MaxPageSize: v.GetInt("server.max_page_size"),
		AllowTables: splitNames(v.GetStringSlice("allow_tables")),
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// splitNames also splits on commas. Viper splits an environment variable on
// spaces only, so LIBSQL_REST_ALLOW_TABLES=users,posts would be one name.
func splitNames(items []string) []string {
	var names []string
	for _, item := range items {
		for name := range strings.SplitSeq(item, ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func serve(ctx context.Context, cfg config.Config, version string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := db.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	st := store.NewSQLStore(sqlDB, schema.NewSQLIntrospector(sqlDB), store.Options{
		MaxPageSize: cfg.MaxPageSize,
		Allow:       cfg.IsTableAllowed,
	})
	h := server.NewHandler(st, server.HandlerConfig{
		TokenRequired: cfg.Auth.Enabled && !cfg.Auth.Optional,
		Version:       version,
	}).Routes()
	if cfg.Auth.Enabled {
		requireToken, err := authMiddleware(cfg.Auth)
		if err != nil {
			return err
		}
		h = requireToken(h)
		slog.Info("jwt authentication enabled", "algorithm", cfg.Auth.Algorithm, "issuer", cfg.Auth.Issuer, "optional", cfg.Auth.Optional)
	}

	srv := server.New(net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), h)
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	slog.Info("listening", "addr", ln.Addr().String(), "version", version)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	stop()
	slog.Info("shutting down; interrupt again to exit at once")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func authMiddleware(cfg config.AuthConfig) (func(http.Handler) http.Handler, error) {
	opts := auth.JWTOptions{
		Algorithm:           cfg.Algorithm,
		Issuer:              cfg.Issuer,
		Audience:            []string{cfg.Audience},
		CredentialsOptional: cfg.Optional,
	}
	if cfg.IsRSA() {
		pemBytes, err := os.ReadFile(cfg.PublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading auth public key: %w", err)
		}
		if opts.RSAPublicKey, err = auth.ParseRSAPublicKey(pemBytes); err != nil {
			return nil, err
		}
	} else {
		opts.HMACSecret = []byte(cfg.Secret)
	}
	return auth.NewJWTMiddleware(opts)
}
