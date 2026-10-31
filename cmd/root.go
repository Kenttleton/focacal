package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/you/focacal/internal/config"
)

var cfgFile string

// rootCmd is the base command for the focacal CLI.
var rootCmd = &cobra.Command{
	Use:   "focacal",
	Short: "focacal — AI-powered calendar focus scheduler",
	Long: `focacal aggregates your Google and Outlook calendars, uses a local AI model
to plan focus blocks and lunch, and serves the result as a CalDAV feed and REST API.

Run 'focacal start' to launch all services.
Run 'focacal auth google <source-id>' to authorize a Google Calendar source.
Run 'focacal auth outlook <source-id>' to authorize an Outlook source.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: /data/config.yaml)")
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug logging")
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
}

// initConfig sets up viper to read config from file and environment.
func initConfig() {
	config.SetDefaults()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Default config path inside the Docker container.
		viper.AddConfigPath("/data")
		// Fallback for local development.
		viper.AddConfigPath("$HOME/.config/focacal")
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Environment variable overrides.
	// e.g. FOCACAL_OLLAMA_BASE_URL overrides ollama.base_url
	viper.SetEnvPrefix("FOCACAL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Also bind common bare env vars that users set directly.
	viper.BindEnv("ollama.base_url", "OLLAMA_BASE_URL")
	viper.BindEnv("oauth.redirect_base_url", "OAUTH_REDIRECT_BASE_URL")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "error reading config: %v\n", err)
		}
		// Missing config file is not fatal — defaults + env vars may be enough.
	}
}
