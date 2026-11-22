package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/birdordev/plumego/security/authn"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type loginProvider interface {
	authorizationURL(state, nonce, verifier string) string
	identity(ctx context.Context, code, verifier, nonce string) (authn.Principal, error)
}

type oidcLogin struct {
	config     oauth2.Config
	verifier   *oidc.IDTokenVerifier
	adminGroup string
}

func newOIDC(ctx context.Context, issuer, clientID, clientSecret, callback, adminGroup string) (*oidcLogin, error) {
	if issuer == "" || clientID == "" || clientSecret == "" {
		return nil, errors.New("OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, errors.New("OIDC provider discovery failed")
	}
	return &oidcLogin{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: callback,
			Endpoint: provider.Endpoint(),
			Scopes:   []string{oidc.ScopeOpenID, "profile"},
		},
		verifier:   provider.Verifier(&oidc.Config{ClientID: clientID}),
		adminGroup: adminGroup,
	}, nil
}

func (o *oidcLogin) authorizationURL(state, nonce, verifier string) string {
	return o.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

func (o *oidcLogin) identity(ctx context.Context, code, verifier, nonce string) (authn.Principal, error) {
	token, err := o.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return authn.Principal{}, errors.New("OIDC code exchange failed")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return authn.Principal{}, errors.New("missing OIDC identity token")
	}
	idToken, err := o.verifier.Verify(ctx, raw)
	if err != nil || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return authn.Principal{}, errors.New("invalid OIDC identity token")
	}
	var claims struct {
		Groups []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil || idToken.Subject == "" {
		return authn.Principal{}, errors.New("invalid OIDC identity claims")
	}
	return mapPrincipal(idToken.Issuer, idToken.Subject, o.adminGroup, claims.Groups), nil
}

func mapPrincipal(issuer, subject, adminGroup string, groups []string) authn.Principal {
	principal := authn.Principal{
		Subject: fmt.Sprintf("%s:%s", issuer, subject),
		Roles:   []string{"member"},
	}
	for _, group := range groups {
		if adminGroup != "" && group == adminGroup {
			principal.Roles = append(principal.Roles, "admin")
			break
		}
	}
	return principal
}
