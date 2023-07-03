package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"github.com/weaveworks/scope/app/httpclient"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"context"
	"github.com/NYTimes/gziphandler"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"

	"github.com/weaveworks/scope/common/hostname"
	"github.com/weaveworks/scope/common/xfer"
	"github.com/weaveworks/scope/report"
)

var (
	// Version - set at buildtime.
	CollectorappIdentity string
	Version              = "dev"
	// UniqueID - set at runtime.
	UniqueID = "0"
)

// contextKey is a wrapper type for use in context.WithValue() to satisfy golint
// https://github.com/golang/go/issues/17293
// https://github.com/golang/lint/pull/245
type contextKey string

// RequestCtxKey is key used for request entry in context
const RequestCtxKey contextKey = contextKey("request")

// CtxHandlerFunc is a http.HandlerFunc, with added contexts
type CtxHandlerFunc func(context.Context, http.ResponseWriter, *http.Request)

func requestContextDecorator(f CtxHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), RequestCtxKey, r)
		f(ctx, w, r)
	}
}

// URLMatcher uses request.RequestURI (the raw, unparsed request) to attempt
// to match pattern.  It does this as go's URL.Parse method is broken, and
// mistakenly unescapes the Path before parsing it.  This breaks %2F (encoded
// forward slashes) in the paths.
func URLMatcher(pattern string) mux.MatcherFunc {
	return func(r *http.Request, rm *mux.RouteMatch) bool {
		vars, match := matchURL(r, pattern)
		if match {
			rm.Vars = vars
		}
		return match
	}
}

func matchURL(r *http.Request, pattern string) (map[string]string, bool) {
	matchParts := strings.Split(pattern, "/")
	path := strings.SplitN(r.RequestURI, "?", 2)[0]
	parts := strings.Split(path, "/")
	if len(parts) != len(matchParts) {
		return nil, false
	}

	vars := map[string]string{}
	for i, part := range parts {
		unescaped, err := url.QueryUnescape(part)
		if err != nil {
			return nil, false
		}
		match := matchParts[i]
		if strings.HasPrefix(match, "{") && strings.HasSuffix(match, "}") {
			vars[strings.Trim(match, "{}")] = unescaped
		} else if matchParts[i] != unescaped {
			return nil, false
		}
	}
	return vars, true
}

func gzipHandler(h http.HandlerFunc) http.Handler {
	return gziphandler.GzipHandler(h)
}

// RegisterTopologyRoutes registers the various topology routes with a http mux.
func RegisterTopologyRoutes(router *mux.Router, r Reporter, capabilities map[string]bool) {
	get := router.Methods("GET").Subrouter()
	get.Handle("/api",
		gzipHandler(requestContextDecorator(apiHandler(r, capabilities))))
	get.Handle("/api/topology",
		gzipHandler(requestContextDecorator(topologyRegistry.makeTopologyList(r))))
	get.Handle("/api/topology/{topology}",
		gzipHandler(requestContextDecorator(topologyRegistry.captureRenderer(r, handleTopology)))).
		Name("api_topology_topology")
	get.Handle("/api/topology/{topology}/ws",
		requestContextDecorator(captureReporter(r, handleWebsocket))). // NB not gzip!
		Name("api_topology_topology_ws")
	get.MatcherFunc(URLMatcher("/api/topology/{topology}/{id}")).Handler(
		gzipHandler(requestContextDecorator(topologyRegistry.captureRenderer(r, handleNode)))).
		Name("api_topology_topology_id")
	get.Handle("/api/report",
		gzipHandler(requestContextDecorator(makeRawReportHandler(r))))
	get.Handle("/api/node/authorize/info",
		gzipHandler(requestContextDecorator(nodeAuthorizeHandler())))
	get.Handle("/api/probes",
		gzipHandler(requestContextDecorator(makeProbeHandler(r))))
}

// Output the current node authorization
func nodeAuthorizeHandler() CtxHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		respondWith(ctx, w, http.StatusOK, httpclient.NodeLimit{
			AutoAuthorize:  AutoAuthorize,
			Limit:          LimitNode,
			Nodes:          AuthorizeNodeList.NodesSlice,
			SlaveCollector: SlaveCollectors.ToSlice(),
		})
	}
}

// RegisterReportPostHandler registers the handler for report submission
type SlaveResp struct {
	List []string
}

func RegisterReportPostHandler(a Adder, router *mux.Router) {
	post := router.Methods("POST").Subrouter()
	post.HandleFunc("/api/report", requestContextDecorator(func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		var (
			buf             = &bytes.Buffer{}
			hasUnAuthorized = false
			reader          = io.TeeReader(r.Body, buf)
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
			respondWith(ctx, w, http.StatusBadRequest, fmt.Errorf("Unsupported Content-Type: %v", contentType))
			return
		}

		rpt, err := report.MakeFromBinary(ctx, reader, gzipped, isMsgpack)
		if err != nil {
			respondWith(ctx, w, http.StatusBadRequest, err)
			return
		}

		// a.Add(..., buf) assumes buf is gzip'd msgpack
		if !isMsgpack {
			buf, _ = rpt.WriteBinary()
		}
		switch LimitNode {
		case LimitUnSet:
			log.Errorf("Error not getting node restrictions")
			w.WriteHeader(http.StatusOK)
			return
		default:
			if res := int(LimitNode) - AuthorizeNodeList.Len(); res < 0 && AuthorizeNodeList.Len() > 0 {
				//当前节点数量大于限制节点数量，则将主动丢弃节点保证数量正确，正常情况下授权数量缩减导致
				for ; res < 0; res++ {
					AuthorizeNodeList.Del()
				}
			}
			var clusterUUID string
			var clusterNodes *Nodes
			var onceDo = sync.Once{}
			var tidy bool
			if len(rpt.Container.Nodes) == 0 && len(rpt.Pod.Nodes) == 0 {
				w.WriteHeader(http.StatusOK)
				return
			}
			for key, node := range rpt.Host.Nodes {
				activeControls, _ := node.Latest.Lookup("active_controls")

				onceDo.Do(func() {
					clusterUUID, _ = node.Latest.Lookup("cluster_uuid")
					clusterNodes = NodeReplenisher.GetNodes(clusterUUID)
					if activeControls == "kubernetes_cordon_node" {
						clusterNodes.PreProcessed()
						*clusterNodes.deleteMark = true
						clusterNodes.deleteMark = NewBoolPoint(false)
					}
				})
				switch activeControls {
				case "host_exec":
					nodeName, _ := node.Latest.Lookup("host_name")
					if nodeName != "" {
						tmpNode := clusterNodes.GetNode(nodeName)
						rpt.Host.Nodes[key] = rpt.Host.Nodes[key].WithLatest("kubernetes.node-role", time.Now(), tmpNode.NodeRole)
					}
					if key != "" {
						//处理节点授权信息
						if AuthorizeNodeList.Exists(key) || LimitNode == UnLimit {
							//授权列表存在即授权
							rpt.Host.Nodes[key] = rpt.Host.Nodes[key].WithLatest("is_authorized", time.Now(), "1")
							break
						}
						if !AutoAuthorize {
							//未开启自动授权
							hasUnAuthorized = true
							rpt.Host.Nodes[key] = rpt.Host.Nodes[key].WithLatest("is_authorized", time.Now(), "0")
							break
						}
						if int(LimitNode) > AuthorizeNodeList.Len() {
							//不限制或者未达到限制上线，则进行授权
							AuthorizeNodeList.Set(key)
							rpt.Host.Nodes[key] = rpt.Host.Nodes[key].WithLatest("is_authorized", time.Now(), "1")
						} else {
							//未授权
							hasUnAuthorized = true
							rpt.Host.Nodes[key] = rpt.Host.Nodes[key].WithLatest("is_authorized", time.Now(), "0")
						}
					}

				case "kubernetes_cordon_node":
					tidy = true
					var nodeName, nodeRole string
					nodeName = strings.Replace(node.ID, ";<host>", "", -1)
					if nodeName != "" {
						nodeRole, _ = node.Latest.Lookup("node_role")
						tmpNode := clusterNodes.GetNode(nodeName)
						tmpNode.NodeRole = nodeRole
						clusterNodes.SetNode(nodeName, tmpNode)
					}
				}
			}
			if tidy {
				NodeReplenisher.Clear <- clusterUUID
			}
			if clusterNodes != nil {
				clusterNodes.nodes.Iter(func(k string, v *Node) (stop bool) {
					fmt.Println(k, v.NodeRole)
					return false
				})
			}
		}

		if hasUnAuthorized {
			tmpRpt := rpt.RetentionElements("host")
			rpt = &tmpRpt
		}
		if err := a.Add(ctx, *rpt, buf.Bytes()); err != nil {
			log.Errorf("Error Adding report: %v", err)
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	post.HandleFunc("/api/slave", requestContextDecorator(func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		//保存所有子收集器的地址
		var (
			resp               = SlaveResp{}
			tmpSlaveCollectors = &SlaveCollector{
				Lock:       sync.RWMutex{},
				Collectors: make(map[string]struct{}),
			}
		)
		log.Info("set slave collector")
		if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		for _, slave := range resp.List {
			if !SlaveCollectors.Exists(slave) {
				//send limit
				pushSlaveSignal <- slave
			}
			tmpSlaveCollectors.Set(slave)
		}
		SlaveCollectors = tmpSlaveCollectors
		w.WriteHeader(http.StatusOK)
	}))
	post.HandleFunc("/api/node/limit", requestContextDecorator(func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		var (
			resp = httpclient.NodeLimit{}
		)
		log.Info("Received node limit request")
		if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		SetNodeLimit(&resp)
		w.WriteHeader(http.StatusOK)
	}))
}

// RegisterAdminRoutes registers routes for admin calls with a http mux.
func RegisterAdminRoutes(router *mux.Router, reporter Reporter) {
	get := router.Methods("GET").Subrouter()
	get.Handle("/admin/summary", requestContextDecorator(func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		summary, err := reporter.AdminSummary(ctx, time.Now())
		if err != nil {
			respondWith(ctx, w, http.StatusBadRequest, err)
		}
		fmt.Fprintln(w, summary)
	}))
}

var newVersion = struct {
	sync.Mutex
	*xfer.NewVersionInfo
}{}

// NewVersion is called to expose new version information to /api
func NewVersion(version, downloadURL string) {
	newVersion.Lock()
	defer newVersion.Unlock()
	newVersion.NewVersionInfo = &xfer.NewVersionInfo{
		Version:     version,
		DownloadURL: downloadURL,
	}
}

func apiHandler(rep Reporter, capabilities map[string]bool) CtxHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) {
		report, err := rep.Report(ctx, time.Now())
		if err != nil {
			respondWith(ctx, w, http.StatusInternalServerError, err)
			return
		}
		newVersion.Lock()
		defer newVersion.Unlock()
		respondWith(ctx, w, http.StatusOK, xfer.Details{
			ID:           UniqueID,
			Version:      Version,
			Hostname:     hostname.Get(),
			Plugins:      report.Plugins,
			Capabilities: capabilities,
			NewVersion:   newVersion.NewVersionInfo,
		})
	}
}
