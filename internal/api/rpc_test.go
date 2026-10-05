package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestServeRPCWritesOneEnvelopeForResultsRefusalsAndBadRequests(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load home config: %v", err)
	}

	for _, tc := range []struct {
		name  string
		input string
		check func(t *testing.T, response api.RPCResponse)
	}{
		{
			name:  "result",
			input: `{"method":"status","params":{}}`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Refusal != nil || response.Error != nil || !json.Valid(response.Result) {
					t.Fatalf("status response = %+v, want only a JSON result", response)
				}
			},
		},
		{
			name:  "refusal",
			input: `{"method":"tasks.get","params":{"number":999}}`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Error != nil || response.Refusal == nil || response.Refusal.Code != model.CodeUnknownTask {
					t.Fatalf("unknown task response = %+v, want only the %q refusal", response, model.CodeUnknownTask)
				}
			},
		},
		{
			name:  "bad request",
			input: `{`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Refusal != nil || response.Error == nil || !response.Error.BadRequest {
					t.Fatalf("malformed request response = %+v, want only a bad-request error", response)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := api.ServeRPC(context.Background(), home.Paths, cfg, bytes.NewBufferString(tc.input), &out); err != nil {
				t.Fatalf("ServeRPC: %v", err)
			}
			decoder := json.NewDecoder(&out)
			var response api.RPCResponse
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("decode rpc response %q: %v", out.String(), err)
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("rpc output contains another response after %q: %v", out.String(), err)
			}
			tc.check(t, response)
		})
	}
}

func TestCommandTransportCarriesOneRequestAndClassifiesAnUnreachableHome(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	machine := testutil.NewClientMachine(t, home)
	cfg, err := config.Load(machine.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}
	transport := api.NewCommandTransport(machine.Paths, cfg, time.Second)

	response, err := transport.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`))
	if err != nil {
		t.Fatalf("round trip to running home: %v", err)
	}
	var envelope api.RPCResponse
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatalf("decode home response %q: %v", response, err)
	}
	if envelope.Refusal != nil || envelope.Error != nil || !json.Valid(envelope.Result) {
		t.Fatalf("running home response = %+v, want only a JSON result", envelope)
	}

	home.Stop()
	_, err = transport.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`))
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("round trip to stopped home error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
}

type w3CountingTransport struct {
	calls  int
	closes int
	err    error
}

func (t *w3CountingTransport) RoundTrip(context.Context, string, []byte) ([]byte, error) {
	t.calls++
	return nil, t.err
}

func (t *w3CountingTransport) Close() error {
	t.closes++
	return nil
}

func TestClientKeepsTheFirstUnreachableHomeForItsLifetime(t *testing.T) {
	transport := &w3CountingTransport{err: &model.Refusal{Code: model.CodeHomeUnreachable, Msg: "the home did not answer"}}
	client := api.NewClient(api.ClientOptions{Transport: transport})

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := client.Status(context.Background())
		refusal, ok := model.AsRefusal(err)
		if !ok || refusal.Code != model.CodeHomeUnreachable {
			t.Fatalf("Status attempt %d error = %v, want %q refusal", attempt, err, model.CodeHomeUnreachable)
		}
	}
	if transport.calls != 1 {
		t.Fatalf("unreachable client called its transport %d times, want one", transport.calls)
	}
}

func TestClientCloseClosesItsTransportOnce(t *testing.T) {
	transport := &w3CountingTransport{}
	client := api.NewClient(api.ClientOptions{Transport: transport})

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if transport.closes != 1 {
		t.Fatalf("Close called the transport %d times, want once", transport.closes)
	}
}
