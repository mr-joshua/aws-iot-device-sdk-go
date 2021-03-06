package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/private/protocol/json/jsonutil"
	ist "github.com/aws/aws-sdk-go/service/iotsecuretunneling"
	"github.com/seqsense/aws-iot-device-sdk-go/v4/tunnel"
)

// apiHandler handles iotsecuretunneling API requests.
type apiHandler struct {
	tunnelHandler *TunnelHandler
	notifier      *Notifier
}

func (h *apiHandler) openTunnel(in *ist.OpenTunnelInput) (*ist.OpenTunnelOutput, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	lifetime := 12 * time.Hour
	if in.TimeoutConfig != nil && in.TimeoutConfig.MaxLifetimeTimeoutMinutes != nil {
		lifetime = time.Minute * time.Duration(*in.TimeoutConfig.MaxLifetimeTimeoutMinutes)
	}

	r1, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	r2, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), lifetime)

	ti := &tunnelInfo{
		thingName:       *in.DestinationConfig.ThingName,
		destAccessToken: r1.String(),
		srcAccessToken:  r2.String(),
		chDone:          ctx.Done(),
		cancel:          cancel,
		chDestSrc:       make(chan []byte),
		chSrcDest:       make(chan []byte),
	}
	for _, srv := range in.DestinationConfig.Services {
		ti.services = append(ti.services, *srv)
	}

	id, err := h.tunnelHandler.add(ti)
	if err != nil {
		return nil, err
	}

	if h.notifier != nil {
		if err := h.notifier.notify(
			context.TODO(),
			*in.DestinationConfig.ThingName,
			&tunnel.Notification{
				ClientAccessToken: ti.destAccessToken,
				ClientMode:        tunnel.Destination,
				Region:            "",
				Services:          ti.services,
			},
		); err != nil {
			return nil, err
		}
	}

	return &ist.OpenTunnelOutput{
		DestinationAccessToken: aws.String(ti.destAccessToken),
		SourceAccessToken:      aws.String(ti.srcAccessToken),
		TunnelArn:              aws.String("arn:clone:iotsecuretunneling:::" + id),
		TunnelId:               aws.String(id),
	}, nil
}

func (h *apiHandler) closeTunnel(in *ist.CloseTunnelInput) (*ist.CloseTunnelOutput, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	id := *in.TunnelId

	if err := h.tunnelHandler.remove(id); err != nil {
		return nil, err
	}

	return &ist.CloseTunnelOutput{}, nil
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(r.Header["X-Amz-Target"]) != 1 {
		http.Error(w, "Failed to handle request", http.StatusBadRequest)
		return
	}
	dec := json.NewDecoder(r.Body)
	var out interface{}

	switch r.Header["X-Amz-Target"][0] {
	case "IoTSecuredTunneling.OpenTunnel":
		in := &ist.OpenTunnelInput{}
		if err := dec.Decode(in); err != nil {
			http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
			return
		}
		var err error
		if out, err = h.openTunnel(in); err != nil {
			http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
			return
		}
	case "IoTSecuredTunneling.CloseTunnel":
		in := &ist.CloseTunnelInput{}
		if err := dec.Decode(in); err != nil {
			http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
			return
		}
		var err error
		if out, err = h.closeTunnel(in); err != nil {
			http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "Failed to handle request", http.StatusBadRequest)
		return
	}
	oj, err := jsonutil.BuildJSON(out)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
		return
	}
	if _, err := w.Write(oj); err != nil {
		http.Error(w, fmt.Sprintf("Failed to handle request: %v", err), http.StatusBadRequest)
		return
	}
}

// NewAPIHandler creates http handler of secure tunnel API.
func NewAPIHandler(tunnelHandler *TunnelHandler, notifier *Notifier) http.Handler {
	return &apiHandler{
		tunnelHandler: tunnelHandler,
		notifier:      notifier,
	}
}
