package vsphere

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type Config struct {
	Endpoint    string   `json:"endpoint"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	VCenterUUID string   `json:"vcenter_uuid"`
	Datacenters []string `json:"datacenters"`
	CAPEM       string   `json:"ca_pem,omitempty"`
}

type reference struct {
	Type string `xml:"type,attr"`
	ID   string `xml:",chardata"`
}

func (r reference) valid(kind string) bool {
	return r.Type == kind && len(r.ID) <= 256 &&
		(referenceID.MatchString(r.ID) || (kind == "ContainerView" && sessionViewID.MatchString(r.ID)))
}

var (
	referenceID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)
	sessionViewID = regexp.MustCompile(`^session\[[A-Za-z0-9_.:-]+\][A-Za-z0-9_.:-]+$`)
	datacenterID  = regexp.MustCompile(`^datacenter-[1-9][0-9]{0,18}$`)
	uuid          = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
)

type serviceContent struct {
	About struct {
		APIType string `xml:"apiType"`
		UUID    string `xml:"instanceUuid"`
	} `xml:"about"`
	Session   reference `xml:"sessionManager"`
	Views     reference `xml:"viewManager"`
	Collector reference `xml:"propertyCollector"`
}

type Client struct {
	endpoint    *url.URL
	http        http.Client
	content     serviceContent
	identity    string
	datacenters map[string]provider.Object
	image       *inventoryImage
}

func Connect(ctx context.Context, cfg Config, transport http.RoundTripper) (_ *Client, result error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") || !uuid.MatchString(cfg.VCenterUUID) ||
		cfg.VCenterUUID == "00000000-0000-0000-0000-000000000000" || len(cfg.Datacenters) < 1 || len(cfg.Datacenters) > 256 ||
		!validCredential(cfg.Username, 512) || !validCredential(cfg.Password, 4096) {
		return nil, errors.New("invalid vSphere profile")
	}
	for i, id := range cfg.Datacenters {
		if !datacenterID.MatchString(id) || slices.Contains(cfg.Datacenters[:i], id) {
			return nil, errors.New("invalid vSphere datacenter scope")
		}
	}
	endpoint.Path = "/sdk"
	if transport == nil {
		transport, err = newTransport(cfg.CAPEM)
		if err != nil {
			return nil, err
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.New("cannot initialize vSphere session")
	}
	c := &Client{endpoint: endpoint, http: http.Client{
		Transport: transport, Jar: jar, Timeout: 25 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			result = errors.Join(result, c.Close(cleanup))
		}
	}()
	var service struct {
		Content serviceContent `xml:"returnval"`
	}
	if err := c.call(ctx, "RetrieveServiceContent", struct {
		Target reference `xml:"_this"`
	}{reference{Type: "ServiceInstance", ID: "ServiceInstance"}}, &service); err != nil {
		return nil, err
	}
	c.content = service.Content
	if c.content.About.APIType != "VirtualCenter" || strings.ToLower(c.content.About.UUID) != cfg.VCenterUUID {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !c.content.Session.valid("SessionManager") || !c.content.Collector.valid("PropertyCollector") || !c.content.Views.valid("ViewManager") {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := c.call(ctx, "Login", struct {
		Target   reference `xml:"_this"`
		Username string    `xml:"userName"`
		Password string    `xml:"password"`
	}{c.content.Session, cfg.Username, cfg.Password}, new(struct{})); err != nil {
		return nil, err
	}
	if !c.hasSession() {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	c.identity = "vsphere:" + cfg.VCenterUUID
	c.datacenters, err = c.verifyDatacenters(ctx, cfg.Datacenters)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Identity() string { return c.identity }

func (c *Client) hasSession() bool {
	return slices.ContainsFunc(c.http.Jar.Cookies(c.endpoint), func(cookie *http.Cookie) bool {
		return cookie.Name == "vmware_soap_session" && cookie.Value != ""
	})
}

func (c *Client) Close(ctx context.Context) error {
	defer c.http.CloseIdleConnections()
	if !c.content.Session.valid("SessionManager") || !c.hasSession() {
		return nil
	}
	err := c.call(ctx, "Logout", struct {
		Target reference `xml:"_this"`
	}{c.content.Session}, new(struct{}))
	if provider.IsFailure(err, provider.Unauthorized) {
		return nil
	}
	return err
}

func validCredential(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n")
}

func newTransport(ca string) (http.RoundTripper, error) {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: dialer.DialContext, TLSHandshakeTimeout: 8 * time.Second,
		ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("invalid vSphere CA bundle")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	return transport, nil
}
