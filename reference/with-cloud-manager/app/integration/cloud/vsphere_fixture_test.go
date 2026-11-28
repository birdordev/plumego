package cloudtest

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const vCenterFixtureUUID = "aced0000-0000-4000-8000-000000000001"
const vSphereFixtureUsername = "ro@example.test"
const vSphereFixturePassword = "fixture<&password"
const vSphereFixtureCookie = "fixture-vsphere-session"
const vSphereFixtureToken = "only-in-session<&vm-page"
const vSphereFixtureViewPrefix = "session[cedf0000-0000-4000-8000-000000000001]view-"

type vSphereFixture struct {
	logins, logouts, destroyed, continues                      atomic.Int32
	wrongUUID, hideDC, failLate, changed, restricted, repeated atomic.Bool
}

type fixtureSOAPRequest struct {
	Body struct {
		Call struct {
			XMLName xml.Name
			Target  struct {
				Type string `xml:"type,attr"`
				ID   string `xml:",chardata"`
			} `xml:"_this"`
			User      string `xml:"userName"`
			Password  string `xml:"password"`
			Token     string `xml:"token"`
			Container string `xml:"container"`
			ViewType  string `xml:"type"`
			Recursive bool   `xml:"recursive"`
			Spec      struct {
				Type    string   `xml:"propSet>type"`
				Paths   []string `xml:"propSet>pathSet"`
				Objects []struct {
					ID     string `xml:"obj"`
					Skip   bool   `xml:"skip"`
					Select struct {
						Schema string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
						Path   string `xml:"path"`
					} `xml:"selectSet"`
				} `xml:"objectSet"`
				ReportMissing bool `xml:"reportMissingObjectsInResults"`
			} `xml:"specSet"`
			MaxObjects int `xml:"options>maxObjects"`
		} `xml:",any"`
	} `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
}

func (f *vSphereFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var req fixtureSOAPRequest
	if r.Method != http.MethodPost || r.URL.RequestURI() != "/sdk" ||
		r.Header.Get("SOAPAction") != `"urn:vim25/7.0"` || xml.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req) != nil ||
		req.Body.Call.XMLName.Space != "urn:vim25" {
		t.Error("invalid vSphere request transport")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	call := req.Body.Call
	method := call.XMLName.Local
	cookie, _ := r.Cookie("vmware_soap_session")
	if method != "RetrieveServiceContent" && method != "Login" && (cookie == nil || cookie.Value != vSphereFixtureCookie) {
		t.Error("vSphere request omitted SOAP session")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	send := func(payload string) {
		w.Header().Set("Content-Type", "text/xml")
		_, err := fmt.Fprintf(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><%sResponse xmlns="urn:vim25">%s</%sResponse></soapenv:Body></soapenv:Envelope>`, method, payload, method)
		if err != nil && r.Context().Err() == nil {
			t.Error(err)
		}
	}
	reject := func(message string) {
		t.Error(message)
		w.WriteHeader(http.StatusBadRequest)
	}
	switch method {
	case "RetrieveServiceContent":
		if call.Target.Type != "ServiceInstance" || call.Target.ID != "ServiceInstance" || cookie != nil {
			reject("service discovery failed identity boundary")
			return
		}
		id := vCenterFixtureUUID
		if f.wrongUUID.Load() {
			id = "aced0000-0000-4000-8000-000000000002"
		}
		send(`<returnval><viewManager type="ViewManager">ViewManager</viewManager>
		<propertyCollector type="PropertyCollector">propertyCollector</propertyCollector>
		<sessionManager type="SessionManager">SessionManager</sessionManager>
		<about><apiType>VirtualCenter</apiType><instanceUuid>` + id + `</instanceUuid></about></returnval>`)
	case "Login":
		if call.Target.Type != "SessionManager" || call.User != vSphereFixtureUsername || call.Password != vSphereFixturePassword || cookie != nil {
			reject("invalid vSphere login")
			return
		}
		f.logins.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "vmware_soap_session", Value: vSphereFixtureCookie, Path: "/sdk", Secure: true, HttpOnly: true})
		send(`<returnval><userName>ro@example.test</userName></returnval>`)
	case "Logout":
		if call.Target.Type != "SessionManager" {
			reject("invalid logout target")
			return
		}
		f.logouts.Add(1)
		send("")
	case "CreateContainerView":
		if call.Target.Type != "ViewManager" || call.Container != "datacenter-7" || !call.Recursive ||
			(call.ViewType != "VirtualMachine" && call.ViewType != "Network") {
			reject("view escaped configured datacenter")
			return
		}
		send(`<returnval type="ContainerView">` + vSphereFixtureViewPrefix + call.ViewType + `</returnval>`)
	case "DestroyView":
		if call.Target.Type != "ContainerView" || !strings.HasPrefix(call.Target.ID, vSphereFixtureViewPrefix) {
			reject("invalid view release")
			return
		}
		f.destroyed.Add(1)
		send("")
	case "RetrievePropertiesEx":
		if call.Target.Type != "PropertyCollector" || !call.Spec.ReportMissing || call.MaxObjects != 250 || len(call.Spec.Objects) != 1 {
			reject("invalid retrieve contract")
			return
		}
		object := call.Spec.Objects[0]
		if call.Spec.Type == "Datacenter" {
			if object.ID != "datacenter-7" || object.Skip || !slices.Equal(call.Spec.Paths, []string{"name"}) {
				reject("unscoped datacenter query")
				return
			}
			if f.hideDC.Load() {
				send("")
			} else {
				send(`<returnval><objects><obj type="Datacenter">datacenter-7</obj><propSet><name>name</name><val>North lab</val></propSet></objects></returnval>`)
			}
			return
		}
		if !object.Skip || object.ID != vSphereFixtureViewPrefix+call.Spec.Type || object.Select.Schema != "TraversalSpec" || object.Select.Path != "view" {
			reject("invalid capability traversal")
			return
		}
		switch call.Spec.Type {
		case "VirtualMachine":
			if !slices.Equal(call.Spec.Paths, []string{"name", "config.instanceUuid", "config.template", "runtime.powerState", "network"}) {
				reject("unexpected VM property selection")
				return
			}
			if f.restricted.Load() {
				send(vSphereVMPage(1, 2, "", false))
			} else {
				send(vSphereVMPage(1, 251, vSphereFixtureToken, false))
			}
		case "Network":
			if !slices.Equal(call.Spec.Paths, []string{"name"}) {
				reject("unexpected network property selection")
				return
			}
			send(`<returnval><objects><obj type="DistributedVirtualPortgroup">dvportgroup-8</obj>
				<propSet><name>name</name><val>Lab private</val></propSet></objects></returnval>`)
		default:
			reject("unexpected collection")
		}
	case "ContinueRetrievePropertiesEx":
		if call.Target.Type != "PropertyCollector" || call.Token != vSphereFixtureToken {
			reject("invalid continuation")
			return
		}
		f.continues.Add(1)
		if f.failLate.Load() {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if f.repeated.Load() {
			send(vSphereVMPage(251, 252, vSphereFixtureToken, false))
		} else {
			send(vSphereVMPage(251, 253, "", f.changed.Load()))
		}
	default:
		reject("unexpected vSphere action " + method)
	}
}

func vSphereVMPage(first, end int, token string, changed bool) string {
	var body strings.Builder
	body.WriteString("<returnval>")
	if token != "" {
		body.WriteString("<token>")
		_ = xml.EscapeText(&body, []byte(token))
		body.WriteString("</token>")
	}
	for i := first; i < end; i++ {
		name := fmt.Sprintf("fixture-vm-%03d", i)
		if i == 251 && changed {
			name = "renamed fixture VM"
		}
		_, _ = fmt.Fprintf(&body, `<objects><obj type="VirtualMachine">vm-%d</obj>
		<propSet><name>name</name><val>%s</val></propSet>
		<propSet><name>config.instanceUuid</name><val>12300000-0000-4000-8000-%012d</val></propSet>
		<propSet><name>config.template</name><val>%t</val></propSet>
		<propSet><name>runtime.powerState</name><val>poweredOff</val></propSet>
		<propSet><name>network</name><val><ManagedObjectReference type="DistributedVirtualPortgroup">dvportgroup-8</ManagedObjectReference></val></propSet>
		</objects>`, i, name, i, i == 252)
	}
	body.WriteString("</returnval>")
	return body.String()
}
