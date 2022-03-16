package probe

import (
	"bytes"
	"compress/gzip"
	"context"
	"github.com/gorilla/mux"
	"github.com/opentracing/opentracing-go"
	log "github.com/sirupsen/logrus"
	"github.com/ugorji/go/codec"
	"github.com/weaveworks/scope/probe/controls"
	"github.com/weaveworks/scope/report"
	"io"
	"net/http"
	"strings"
)

func respondWith(ctx context.Context, w http.ResponseWriter, code int, response interface{}) {
	if err, ok := response.(error); ok {
		log.Errorf("Error %d: %v", code, err)
		response = err.Error()
	} else if 500 <= code && code < 600 {
		log.Errorf("Non-error %d: %v", code, response)
	} else if ctx.Err() != nil {
		log.Debugf("Context error %v", ctx.Err())
		code = 499
		response = nil
	}
	if span := opentracing.SpanFromContext(ctx); span != nil {
		span.LogKV("response-code", code)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(code)
	encoder := codec.NewEncoder(w, &codec.JsonHandle{})
	if err := encoder.Encode(response); err != nil {
		log.Errorf("Error encoding response: %v", err)
	}
}

type contextKey string

const RequestCtxKey contextKey = contextKey("request")

type CtxHandlerFunc func(context.Context, http.ResponseWriter, *http.Request)

func requestContextDecorator(f CtxHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), RequestCtxKey, r)
		f(ctx, w, r)
	}
}
func RegisterRedirectReportPostHandler(router *mux.Router, client interface {
	ReportPublisher
	controls.PipeClient
}) {
	post := router.Methods("POST").Subrouter()
	post.HandleFunc("/api/redirect-report", requestContextDecorator(func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		var (
			buf    = &bytes.Buffer{}
			reader = io.TeeReader(r.Body, buf)
		)
		gzipped := strings.Contains(r.Header.Get("Content-Encoding"), "gzip")
		if !gzipped {
			reader = io.TeeReader(r.Body, gzip.NewWriter(buf))
		}
		contentType := r.Header.Get("Content-Type")
		var isMsgpack bool
		switch {
		case strings.HasPrefix(contentType, "application/msgpack"):
			isMsgpack = true
		case strings.HasPrefix(contentType, "application/json"):
			isMsgpack = false
		default:
			return
		}
		rpt, err := report.MakeFromBinary(context.Background(), reader, gzipped, isMsgpack)
		if err != nil {
			log.Error("Redirect Report Error:", err)
			return
		}
		log.Info("Redirect report start")
		err = client.Publish(*rpt)
		if err != nil {
			log.Error("Redirect report publish error:", err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}
