package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jfrog/jfrog-cli-core/v2/plugins/components"
	"github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	"github.com/jfrog/jfrog-client-go/utils/log"
)

func GetCaExtensionCommand() components.Command {
	return components.Command{
		Name:        "ca-extension",
		Description: "Curation Audit Extension to unofficially support for new package managers.",
		Aliases:     []string{"cae"},
		Arguments:   getCaExtensionArguments(),
		Flags:       getCaExtensionFlags(),
		EnvVars:     getCaExtensionEnvVar(),
		Action: func(c *components.Context) error {
			return CaExtensionCmd(c)
		},
	}
}

func getCaExtensionArguments() []components.Argument {
	return []components.Argument{
		{
			Name:        "package-manager",
			Description: "The name of the package manager to audit",
		},
		{
			Name:        "repository-name",
			Description: "The JFrog Repository Name",
		},
		{
			Name:        "lock-file",
			Description: "The path to the lock file to audit",
		},
		{
			Name:        "access-token",
			Description: "JFrog Access Token",
		},
	}
}

func getCaExtensionFlags() []components.Flag {

	return []components.Flag{
		components.NewBoolFlag(
			"shout",
			"Makes output uppercase",
			components.WithBoolDefaultValue(false),
		),
	}
}

func getCaExtensionEnvVar() []components.EnvVar {
	return []components.EnvVar{
		{
			Name:        "HELLO_FROG_GREET_PREFIX",
			Default:     "A new greet from your plugin template: ",
			Description: "Adds a prefix to every greet.",
		},
	}
}

type CaExtensionConfiguration struct {
	addressee string
	shout     bool
	prefix    string
}

func CaExtensionCmd(c *components.Context) error {
	if len(c.Arguments) == 0 {
		message := "Hello :) Now try adding an argument to the 'hi' command"
		// You log messages using the following log levels.
		log.Output(message)
		log.Debug(message)
		log.Info(message)
		log.Warn(message)
		log.Error(message)
		return nil
	}
	if len(c.Arguments) > 1 {
		return errors.New("too many arguments received. Now run the command again, with one argument only")
	}

	var conf = new(CaExtensionConfiguration)
	conf.addressee = c.Arguments[0]
	conf.shout = c.GetBoolFlagValue("shout")
	conf.prefix = os.Getenv("HELLO_FROG_GREET_PREFIX")
	if conf.prefix == "" {
		conf.prefix = "New greeting: "
	}

	log.Info(CaExtensionGreet(conf))

	if !conf.shout {
		message := "Now try adding the --shout option to the command"
		log.Info(message)
		return nil
	}

	if os.Getenv(coreutils.LogLevel) == "" {
		message := fmt.Sprintf("Now try setting the %s environment variable to %s and run the command again", coreutils.LogLevel, "DEBUG")
		log.Info(message)
	}
	return nil
}

func CaExtensionGreet(c *CaExtensionConfiguration) string {
	greet := c.prefix + "Hello " + c.addressee + "\n"

	if c.shout {
		greet = strings.ToUpper(greet)
	}

	return strings.TrimSpace(greet)
}
