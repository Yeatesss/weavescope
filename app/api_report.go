package app

import (
	"net/http"
	"strings"
	"time"

	"context"

	"github.com/weaveworks/scope/report"
)

// Raw report handler
func makeRawReportHandler(rep Reporter) CtxHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		//only := r.URL.Query().Get("only")

		timestamp := deserializeTimestamp(r.URL.Query().Get("timestamp"))
		rawReport, err := rep.Report(ctx, timestamp)
		if err != nil {
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		if removes := r.URL.Query().Get("remove"); removes != "" {
			removeSlice := strings.Split(removes, ",")
			Remove(&rawReport, removeSlice)
		}
		censorCfg := report.GetCensorConfigFromRequest(r)
		respondWithReport(ctx, w, r, report.CensorRawReport(rawReport, censorCfg))
	}
}
func Remove(rpt *report.Report, rms []string) {
	for _, r := range rms {
		switch r {
		case "endpoint":
			rpt.Endpoint.Nodes = make(report.Nodes)
		}
	}
}

type probeDesc struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname"`
	Version  string    `json:"version"`
	LastSeen time.Time `json:"lastSeen"`
}

// Probe handler
func makeProbeHandler(rep Reporter) CtxHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if _, sparse := r.Form["sparse"]; sparse {
			// if we have reports, we must have connected probes
			hasProbes, err := rep.HasReports(ctx, time.Now())
			if err != nil {
				respondWith(ctx, w, http.StatusInternalServerError, err)
			}
			respondWith(ctx, w, http.StatusOK, hasProbes)
			return
		}
		rpt, err := rep.Report(ctx, time.Now())
		if err != nil {
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		result := []probeDesc{}
		for _, n := range rpt.Host.Nodes {
			id, _ := n.Latest.Lookup(report.ControlProbeID)
			hostname, _ := n.Latest.Lookup(report.HostName)
			version, dt, _ := n.Latest.LookupEntry(report.ScopeVersion)
			result = append(result, probeDesc{
				ID:       id,
				Hostname: hostname,
				Version:  version,
				LastSeen: dt,
			})
		}
		respondWith(ctx, w, http.StatusOK, result)
	}
}
