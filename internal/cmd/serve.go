package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/db"
	"github.com/JayJamieson/libsql-rest/internal/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var port int
var host string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "start a libsql-rest server",
	Long:  `start a libsql-rest server`,
	RunE: func(cmd *cobra.Command, args []string) error {

		cfg := &server.Config{
			Port: viper.GetInt("server.port"),
			Host: viper.GetString("server.host"),
		}

		sqlDb, err := db.New(fmt.Sprintf("%s?authToken=%s", viper.GetString("db.uri"), viper.GetString("db.token")))

		if err != nil {
			return err
		}

		s, _ := server.New(cfg, sqlDb)

		done := make(chan os.Signal, 1)
		signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			if err := s.Start(); err != nil {
				log.Fatal("error starting server", "err", err)
			}
		}()

		<-done

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer func() { cancel() }()

		return s.Shutdown(ctx)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntVarP(
		&port,
		"port",
		"p",
		8080,
		"Port to start libsql-rest server on.")
	serveCmd.Flags().StringVar(
		&host,
		"host",
		"",
		"Port to start libsql-rest server on.")

	viper.BindPFlag("server.host", serveCmd.Flags().Lookup("host"))
	viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
	viper.BindPFlag("server.port", serveCmd.Flags().ShorthandLookup("p"))

}
