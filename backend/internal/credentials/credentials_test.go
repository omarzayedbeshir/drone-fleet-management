package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestCredentialsNeverPrintSecrets(t *testing.T) {
	c := New(map[string]string{"API_KEY": "s3cret-value"})
	for _, s := range []string{
		fmt.Sprint(c), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c),
	} {
		if strings.Contains(s, "s3cret") {
			t.Fatalf("secret leaked: %s", s)
		}
	}
	if v := c.LogValue(); strings.Contains(v.String(), "s3cret") {
		t.Fatal("slog value leaked secret")
	}
	var _ slog.LogValuer = c
}

func TestGetIsCaseInsensitiveAndRequire(t *testing.T) {
	c := New(map[string]string{"API_KEY": "abc", "empty": ""})
	if v, ok := c.Get("api_key"); !ok || v != "abc" {
		t.Fatalf("Get = %q,%v", v, ok)
	}
	if _, err := c.Require("missing"); err == nil {
		t.Fatal("Require(missing) should fail")
	}
	if _, err := c.Require("empty"); err == nil {
		t.Fatal("Require(empty) should fail")
	}
	if err := func() error { _, err := c.Require("missing"); return err }(); strings.Contains(err.Error(), "abc") {
		t.Fatal("error must not contain secret values")
	}
	if got := strings.Join(c.Keys(), ","); got != "api_key,empty" {
		t.Fatalf("Keys = %s", got)
	}
}

func TestStaticProvider(t *testing.T) {
	p := StaticProvider{"dji": {"api_key": "k"}}
	if _, err := p.Get(context.Background(), "dji"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnvProvider(t *testing.T) {
	p := EnvProvider{Environ: func() []string {
		return []string{
			"AEROSYNC_DJI_API_KEY=abc",
			"AEROSYNC_DJI_REGION=eu",
			"AEROSYNC_DJI_EMPTY=",
			"AEROSYNC_OTHER_API_KEY=zzz",
			"PATH=/bin",
			"malformed",
		}
	}}
	c, err := p.Get(context.Background(), "dji")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Keys(), ","); got != "api_key,region" {
		t.Fatalf("keys = %s", got)
	}
	if _, err := p.Get(context.Background(), "mavlink"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnvProviderCustomPrefixAndDefaultEnviron(t *testing.T) {
	t.Setenv("XYZ_DJI_API_KEY", "v")
	c, err := EnvProvider{Prefix: "XYZ_"}.Get(context.Background(), "dji")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get("api_key"); v != "v" {
		t.Fatalf("got %q", v)
	}
}
