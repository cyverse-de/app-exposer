package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/app-exposer/external"
	"github.com/knadh/koanf"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewExposerApp(t *testing.T) {
	expectedNS := "testing"
	testcs := fake.NewSimpleClientset()

	testinit := &ExposerAppInit{
		Namespace:     expectedNS,
		ViceNamespace: "",
		IngressClass:  "linkerd",
		ClientSet:     testcs,
	}
	c := koanf.New(".")

	testapp := NewExposerApp(testinit, nil, c)

	if testapp.namespace != expectedNS {
		t.Errorf("namespace was %s, not %s", testapp.namespace, expectedNS)
	}

	if testapp.external.ServiceController == nil {
		t.Error("ServiceController is nil")
	}

	if testapp.external.EndpointController == nil {
		t.Error("EndpointController is nil")
	}

	if testapp.external.IngressController == nil {
		t.Error("IngressController is nil")
	}

	if testapp.router == nil {
		t.Error("router is nil")
	}
}

func TestCreateService(t *testing.T) {
	expectedNS := "testing"
	testcs := fake.NewSimpleClientset()

	testinit := &ExposerAppInit{
		Namespace:     expectedNS,
		ViceNamespace: "",
		IngressClass:  "linkerd",
		ClientSet:     testcs,
	}
	c := koanf.New(".")

	testapp := NewExposerApp(testinit, nil, c)

	expectedOpts := &external.ServiceOptions{
		TargetPort: 60000,
		ListenPort: 60001,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	expectedName := "test-name"
	req := httptest.NewRequest("POST", fmt.Sprintf("/service/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.ServiceOptions{}
	err = json.Unmarshal(rbody, actualOpts)
	if err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("service name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("service namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.TargetPort != expectedOpts.TargetPort {
		t.Errorf("service target port was %d, not %d", actualOpts.TargetPort, expectedOpts.TargetPort)
	}

	if actualOpts.ListenPort != expectedOpts.ListenPort {
		t.Errorf("service listen port was %d, not %d", actualOpts.ListenPort, expectedOpts.ListenPort)
	}
}

// createLoadApp is a utility function that creates a new ExposerApp, loads a
// service into it, and returns the app.
func createAppLoadService(ns, name string) (*ExposerApp, error) {
	testcs := fake.NewSimpleClientset()

	testinit := &ExposerAppInit{
		Namespace:     ns,
		ViceNamespace: "",
		ClientSet:     testcs,
		IngressClass:  "linkerd",
	}

	c := koanf.New(".")

	testapp := NewExposerApp(testinit, nil, c)

	createOpts := &external.ServiceOptions{
		TargetPort: 40000,
		ListenPort: 40001,
	}

	createJSON, err := json.Marshal(createOpts)
	if err != nil {
		return nil, err
	}

	addreq, err := http.NewRequest("POST", fmt.Sprintf("/service/%s", name), bytes.NewReader(createJSON))
	addreq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err != nil {
		return nil, err
	}

	cw := httptest.NewRecorder()

	testapp.router.ServeHTTP(cw, addreq)

	return testapp, nil
}

func TestUpdateService(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"

	testapp, err := createAppLoadService(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	expectedOpts := &external.ServiceOptions{
		TargetPort: 60000,
		ListenPort: 60001,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("PUT", fmt.Sprintf("/service/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err != nil {
		t.Error(err)
	}
	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.ServiceOptions{}
	err = json.Unmarshal(rbody, actualOpts)
	if err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("service name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("service namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.TargetPort != expectedOpts.TargetPort {
		t.Errorf("service target port was %d, not %d", actualOpts.TargetPort, expectedOpts.TargetPort)
	}

	if actualOpts.ListenPort != expectedOpts.ListenPort {
		t.Errorf("service listen port was %d, not %d", actualOpts.ListenPort, expectedOpts.ListenPort)
	}
}

func TestGetService(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"

	testapp, err := createAppLoadService(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	expectedOpts := &external.ServiceOptions{
		TargetPort: 40000,
		ListenPort: 40001,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("/service/%s", expectedName), bytes.NewReader(expectedJSON))
	if err != nil {
		t.Error(err)
	}
	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.ServiceOptions{}
	err = json.Unmarshal(rbody, actualOpts)
	if err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("service name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("service namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.TargetPort != expectedOpts.TargetPort {
		t.Errorf("service target port was %d, not %d", actualOpts.TargetPort, expectedOpts.TargetPort)
	}

	if actualOpts.ListenPort != expectedOpts.ListenPort {
		t.Errorf("service listen port was %d, not %d", actualOpts.ListenPort, expectedOpts.ListenPort)
	}
}

func TestDeleteService(t *testing.T) {
	ns := "testing"
	name := "test-name"

	testapp, err := createAppLoadService(ns, name)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("/service/%s", name), nil)
	if err != nil {
		t.Error(err)
	}

	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()

	if resp.StatusCode != 200 {
		t.Errorf("status code was %d not 200", resp.StatusCode)
	}
}

func TestCreateEndpoint(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedIP := "1.1.1.1"
	var expectedPort int32 = 60000

	testcs := fake.NewSimpleClientset()

	testinit := &ExposerAppInit{
		Namespace:     expectedNS,
		ViceNamespace: "",
		ClientSet:     testcs,
		IngressClass:  "linkerd",
	}

	c := koanf.New(".")

	testapp := NewExposerApp(testinit, nil, c)

	expectedOpts := &external.EndpointOptions{
		IP:   expectedIP,
		Port: expectedPort,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req := httptest.NewRequest("POST", fmt.Sprintf("/endpoint/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.EndpointOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("endpoint name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("endpoint namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.IP != expectedIP {
		t.Errorf("endpoint ip was %s, not %s", actualOpts.IP, expectedIP)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("expoint port %d, not %d", actualOpts.Port, expectedPort)
	}
}

func createAppLoadEndpoint(ns, name string) (*ExposerApp, error) {
	testcs := fake.NewSimpleClientset()
	testinit := &ExposerAppInit{
		Namespace:     ns,
		ViceNamespace: "",
		ClientSet:     testcs,
		IngressClass:  "linkerd",
	}
	c := koanf.New(".")
	testapp := NewExposerApp(testinit, nil, c)

	createOpts := &external.EndpointOptions{
		IP:   "1.1.1.1",
		Port: 60000,
	}

	createJSON, err := json.Marshal(createOpts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("/endpoint/%s", name), bytes.NewReader(createJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err != nil {
		return nil, err
	}

	cw := httptest.NewRecorder()

	testapp.router.ServeHTTP(cw, req)

	return testapp, nil
}

func TestUpdateEndpoint(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedIP := "1.1.1.2"
	var expectedPort int32 = 60001

	testapp, err := createAppLoadEndpoint(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	expectedOpts := &external.EndpointOptions{
		IP:   expectedIP,
		Port: expectedPort,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("PUT", fmt.Sprintf("/endpoint/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if err != nil {
		t.Error(err)
	}

	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.EndpointOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("endpoint name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("service namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.IP != expectedIP {
		t.Errorf("endpoint ip was %s, not %s", actualOpts.IP, expectedIP)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("expoint port %d, not %d", actualOpts.Port, expectedPort)
	}
}

func TestGetEndpoint(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedIP := "1.1.1.1"
	var expectedPort int32 = 60000

	testapp, err := createAppLoadEndpoint(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("/endpoint/%s", expectedName), nil)
	if err != nil {
		t.Error(err)
	}

	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.EndpointOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("endpoint name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("service namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.IP != expectedIP {
		t.Errorf("endpoint ip was %s, not %s", actualOpts.IP, expectedIP)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("expoint port %d, not %d", actualOpts.Port, expectedPort)
	}
}

func TestDeleteEndpoint(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"

	testapp, err := createAppLoadEndpoint(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("/endpoint/%s", expectedName), nil)
	if err != nil {
		t.Error(err)
	}

	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()

	if resp.StatusCode != 200 {
		t.Errorf("status code was %d not 200", resp.StatusCode)
	}
}

func TestCreateIngress(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedService := "test-service"
	expectedPort := 60000

	testcs := fake.NewSimpleClientset()
	testinit := &ExposerAppInit{
		Namespace:     expectedNS,
		ViceNamespace: "",
		ClientSet:     testcs,
		IngressClass:  "linkerd",
	}

	c := koanf.New(".")

	testapp := NewExposerApp(testinit, nil, c)

	expectedOpts := &external.IngressOptions{
		Service: expectedService,
		Port:    expectedPort,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req := httptest.NewRequest("POST", fmt.Sprintf("/ingress/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.IngressOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("ingress name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("ingress namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.Service != expectedService {
		t.Errorf("ingress service was %s, not %s", actualOpts.Service, expectedService)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("ingress port was %d, not %d", actualOpts.Port, expectedPort)
	}
}

func createAppLoadIngress(ns, name string) (*ExposerApp, error) {
	testcs := fake.NewSimpleClientset()
	testinit := &ExposerAppInit{
		Namespace:     ns,
		ViceNamespace: "",
		ClientSet:     testcs,
		IngressClass:  "linkerd",
	}
	c := koanf.New(".")
	testapp := NewExposerApp(testinit, nil, c)

	createOpts := &external.IngressOptions{
		Service: "test-service",
		Port:    60000,
	}

	createJSON, err := json.Marshal(createOpts)
	if err != nil {
		return nil, err
	}

	req := httptest.NewRequest("POST", fmt.Sprintf("/ingress/%s", name), bytes.NewReader(createJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	cw := httptest.NewRecorder()
	ctx := testapp.router.NewContext(req, cw)
	ctx.SetPath("/ingress/:name")
	ctx.SetParamNames("name")
	ctx.SetParamValues(name)
	if err = testapp.external.CreateIngressHandler(ctx); err != nil {
		return nil, err
	}

	return testapp, nil
}

func TestUpdateIngress(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedService := "test-service1"
	expectedPort := 60001

	testapp, err := createAppLoadIngress(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	expectedOpts := &external.IngressOptions{
		Service: expectedService,
		Port:    expectedPort,
	}

	expectedJSON, err := json.Marshal(expectedOpts)
	if err != nil {
		t.Error(err)
	}

	req := httptest.NewRequest("PUT", fmt.Sprintf("/ingress/%s", expectedName), bytes.NewReader(expectedJSON))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

	w := httptest.NewRecorder()
	c := testapp.router.NewContext(req, w)
	c.SetPath("/ingress/:name")
	c.SetParamNames("name")
	c.SetParamValues(expectedName)

	if err = testapp.external.UpdateIngressHandler(c); err != nil {
		t.Error(err)
	}

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.IngressOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("ingress name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("ingress namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.Service != expectedService {
		t.Errorf("ingress service was %s, not %s", actualOpts.Service, expectedService)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("ingress port was %d, not %d", actualOpts.Port, expectedPort)
	}
}

func TestGetIngress(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"
	expectedService := "test-service"
	expectedPort := 60000

	testapp, err := createAppLoadIngress(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/ingress/%s", expectedName), nil)

	w := httptest.NewRecorder()
	c := testapp.router.NewContext(req, w)
	c.SetPath("/ingress/:name")
	c.SetParamNames("name")
	c.SetParamValues(expectedName)

	err = testapp.external.GetIngressHandler(c)
	if err != nil {
		t.Error(err)
	}

	resp := w.Result()
	rbody, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		t.Error(err)
	}

	actualOpts := &external.IngressOptions{}
	if err = json.Unmarshal(rbody, actualOpts); err != nil {
		t.Error(err)
	}

	if actualOpts.Name != expectedName {
		t.Errorf("ingress name was %s, not %s", actualOpts.Name, expectedName)
	}

	if actualOpts.Namespace != expectedNS {
		t.Errorf("ingress namespace was %s, not %s", actualOpts.Namespace, expectedNS)
	}

	if actualOpts.Service != expectedService {
		t.Errorf("ingress service was %s, not %s", actualOpts.Service, expectedService)
	}

	if actualOpts.Port != expectedPort {
		t.Errorf("ingress port was %d, not %d", actualOpts.Port, expectedPort)
	}
}

func TestDeleteIngress(t *testing.T) {
	expectedNS := "testing"
	expectedName := "test-name"

	testapp, err := createAppLoadIngress(expectedNS, expectedName)
	if err != nil {
		t.Error(err)
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("/ingress/%s", expectedName), nil)
	if err != nil {
		t.Error(err)
	}

	w := httptest.NewRecorder()

	testapp.router.ServeHTTP(w, req)

	resp := w.Result()

	if resp.StatusCode != 200 {
		t.Errorf("status code was %d not 200", resp.StatusCode)
	}
}
