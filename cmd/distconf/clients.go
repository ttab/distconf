package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ttab/clitools"
	"github.com/ttab/distconf"
	dist "github.com/ttab/elephant-public-api/distribution"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

func getClients(
	ctx context.Context, c *cli.Command,
) (*distconf.StaticClients, error) {
	clientID := c.String("client-id")
	clientSecret := c.String("client-secret")
	env := c.String("env")

	if clientID == "" {
		clientID = clitools.DefaultApplicationID
	}

	conf, err := clitools.NewConfigurationHandler(
		appName, clientID, env,
	)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	endpoint, ok := conf.GetEndpoint("distribution")
	if !ok {
		return nil, errors.New(
			"no distribution endpoint configured for environment")
	}

	var token oauth2.TokenSource

	scopes := []string{"dist_admin"}

	if clientSecret != "" {
		t, err := conf.GetClientAccessToken(
			ctx, clientID, clientSecret, scopes)
		if err != nil {
			return nil, fmt.Errorf(
				"get client access token: %w", err)
		}

		token = t
	} else {
		t, err := conf.GetAccessToken(ctx, scopes)
		if err != nil {
			return nil, fmt.Errorf("get access token: %w", err)
		}

		err = conf.Save()
		if err != nil {
			slog.Warn("save configuration",
				"err", err)
		}

		token = oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: t.Token,
		})
	}

	client := oauth2.NewClient(ctx, token)

	return &distconf.StaticClients{
		Configuration: dist.NewConfigurationProtobufClient(
			endpoint, client),
	}, nil
}
