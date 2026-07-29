package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ttab/clitools"
	"github.com/ttab/distconf/distribution"
	"github.com/ttab/distconf/live"
	dist "github.com/ttab/elephant-public-api/distribution"
	liveapi "github.com/ttab/elephant-public-api/live"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

// getDistributionClients creates clients for the distribution service.
func getDistributionClients(
	ctx context.Context, c *cli.Command,
) (*distribution.StaticClients, error) {
	endpoint, client, err := getEndpointClient(
		ctx, c, distribution.ServiceName, []string{"dist_admin"})
	if err != nil {
		return nil, err
	}

	return &distribution.StaticClients{
		Configuration: dist.NewConfigurationProtobufClient(
			endpoint, client),
	}, nil
}

// getLiveClients creates clients for the live service.
func getLiveClients(
	ctx context.Context, c *cli.Command,
) (*live.StaticClients, error) {
	endpoint, client, err := getEndpointClient(
		ctx, c, live.ServiceName, []string{"liveblog_admin"})
	if err != nil {
		return nil, err
	}

	return &live.StaticClients{
		Configuration: liveapi.NewConfigurationProtobufClient(
			endpoint, client),
	}, nil
}

// getEndpointClient resolves the endpoint for the named service in the
// selected environment and returns it together with an authorized HTTP
// client requesting the given scopes.
func getEndpointClient(
	ctx context.Context, c *cli.Command,
	endpointName string, scopes []string,
) (string, *http.Client, error) {
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
		return "", nil, fmt.Errorf("load configuration: %w", err)
	}

	endpoint, ok := conf.GetEndpoint(endpointName)
	if !ok {
		return "", nil, fmt.Errorf(
			"no %s endpoint configured for environment",
			endpointName)
	}

	var token oauth2.TokenSource

	if clientSecret != "" {
		t, err := conf.GetClientAccessToken(
			ctx, clientID, clientSecret, scopes)
		if err != nil {
			return "", nil, fmt.Errorf(
				"get client access token: %w", err)
		}

		token = t
	} else {
		t, err := conf.GetAccessToken(ctx, scopes)
		if err != nil {
			return "", nil, fmt.Errorf("get access token: %w", err)
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

	return endpoint, oauth2.NewClient(ctx, token), nil
}
