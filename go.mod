module github.com/weaveworks/scope

go 1.18

require (
	camlistore.org v0.0.0-20171230002226-a5a65f0d8b22
	github.com/NYTimes/gziphandler v1.1.1
	github.com/Yeatesss/container-software v0.0.0-20230825021639-6ddb682d79c2
	github.com/armon/go-radix v0.0.0-20180808171621-7fddfc383310
	github.com/armon/go-socks5 v0.0.0-20160902184237-e75332964ef5
	github.com/bluele/gcache v0.0.0-20150827032927-fb6c0b0e1ff0
	github.com/c9s/goprocinfo v0.0.0-20151025191153-19cb9f127a9c
	github.com/certifi/gocertifi v0.0.0-20200922220541-2c3bb06c6054
	github.com/coocood/freecache v1.2.3
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/docker v24.0.6+incompatible // indirect
	github.com/dolthub/swiss v0.1.1-0.20230403225903-a08bdf4889f1
	github.com/dustin/go-humanize v1.0.0
	github.com/fsouza/go-dockerclient v1.3.0
	github.com/gogo/protobuf v1.3.2
	github.com/goji/httpauth v0.0.0-20160601135302-2da839ab0f4d
	github.com/golang/protobuf v1.5.3
	github.com/google/gopacket v1.1.17
	github.com/gorilla/handlers v0.0.0-20151024084542-9a8d6fa6e647
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.4.2
	github.com/hashicorp/go-cleanhttp v0.5.2
	github.com/json-iterator/go v1.1.12
	github.com/k-sone/critbitgo v1.2.0
	github.com/mgutz/ansi v0.0.0-20170206155736-9520e82c474b
	github.com/miekg/dns v1.1.25-0.20191211073109-8ebf2e419df7
	github.com/mjibson/esc v0.2.0
	github.com/opentracing/opentracing-go v1.1.0
	github.com/paypal/ionet v0.0.0-20130919195445-ed0aaebc5417
	github.com/pborman/uuid v0.0.0-20150824212802-cccd189d45f7
	github.com/peterbourgon/runsvinit v2.0.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/richo/GOSHOUT v0.0.0-20210103052837-9a2e452d4c18
	github.com/sirupsen/logrus v1.9.3
	github.com/spaolacci/murmur3 v1.1.0
	github.com/stretchr/testify v1.8.3
	github.com/tylerb/graceful v1.2.13
	github.com/typetypetype/conntrack v1.0.1-0.20181112022515-9d9dd841d4eb
	github.com/vishvananda/netlink v1.2.1-beta.2
	github.com/vishvananda/netns v0.0.4
	github.com/weaveworks/common v0.0.0-20200310113808-2708ba4e60a4
	github.com/weaveworks/ps v0.0.0-20160725183535-70d17b2d6f76
	github.com/weaveworks/tcptracer-bpf v0.0.0-20200114145059-84a08fc667c0
	github.com/weaveworks/weave v2.6.4+incompatible
	github.com/willdonnelly/passwd v0.0.0-20141013001024-7935dab3074c
	golang.org/x/net v0.15.0
	golang.org/x/sys v0.12.0
	golang.org/x/time v0.3.0
	golang.org/x/tools v0.11.0
	google.golang.org/grpc v1.56.2
	k8s.io/api v0.26.3
	k8s.io/apimachinery v0.26.3
	k8s.io/client-go v0.26.3
)

require (
	github.com/Microsoft/hcsshim v0.11.0
	github.com/Yeatesss/go-codec v0.0.0-20230824130049-16b87d0b55b1
	github.com/charmbracelet/log v0.2.2
	github.com/containerd/cgroups v1.1.0
	github.com/containerd/cgroups/v3 v3.0.2
	github.com/containerd/containerd v1.7.6
	github.com/containerd/typeurl v1.0.3-0.20220422153119-7f6e6d160d67
	github.com/containerd/typeurl/v2 v2.1.1
	github.com/klauspost/compress v1.17.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0-rc5
	github.com/pyroscope-io/client v0.7.1
	golang.org/x/sync v0.3.0
)

require github.com/containerd/nerdctl v1.6.0

require (
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230811130428-ced1acdcaa24 // indirect
	github.com/AdamKorcz/go-118-fuzz-build v0.0.0-20230306123547-8075edf89bb0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/Masterminds/semver/v3 v3.2.1 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/awslabs/soci-snapshotter v0.4.0 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/charmbracelet/lipgloss v0.7.1 // indirect
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/containerd/console v1.0.3 // indirect
	github.com/containerd/continuity v0.4.2 // indirect
	github.com/containerd/fifo v1.1.0 // indirect
	github.com/containerd/go-cni v1.1.9 // indirect
	github.com/containerd/imgcrypt v1.1.8 // indirect
	github.com/containerd/stargz-snapshotter v0.14.3 // indirect
	github.com/containerd/ttrpc v1.2.2 // indirect
	github.com/containernetworking/cni v1.1.2 // indirect
	github.com/containernetworking/plugins v1.3.0 // indirect
	github.com/containers/ocicrypt v1.1.8 // indirect
	github.com/coreos/go-iptables v0.7.0 // indirect
	github.com/docker/cli v24.0.6+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.7.0 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dolthub/maphash v0.1.0 // indirect
	github.com/emicklei/go-restful/v3 v3.10.1 // indirect
	github.com/go-jose/go-jose/v3 v3.0.0 // indirect
	github.com/go-kit/kit v0.9.0 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/swag v0.19.14 // indirect
	github.com/gogo/googleapis v1.4.0 // indirect
	github.com/gogo/status v1.0.3 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/iovisor/gobpf v0.0.0-20180826141936-4ece6c56f936 // indirect
	github.com/ipfs/go-cid v0.4.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/cpuid/v2 v2.1.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/miekg/pkcs11 v1.1.1 // indirect
	github.com/minio/sha256-simd v1.0.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/patternmatcher v0.5.0 // indirect
	github.com/moby/sys/mountinfo v0.6.2 // indirect
	github.com/moby/sys/sequential v0.5.0 // indirect
	github.com/moby/sys/signal v0.7.0 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/mr-tron/base58 v1.2.0 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/muesli/termenv v0.15.1 // indirect
	github.com/multiformats/go-base32 v0.1.0 // indirect
	github.com/multiformats/go-base36 v0.1.0 // indirect
	github.com/multiformats/go-multibase v0.1.1 // indirect
	github.com/multiformats/go-multihash v0.2.1 // indirect
	github.com/multiformats/go-varint v0.0.6 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/runc v1.1.7 // indirect
	github.com/opencontainers/runtime-spec v1.1.0 // indirect
	github.com/opencontainers/selinux v1.11.0 // indirect
	github.com/opentracing-contrib/go-stdlib v0.0.0-20190519235532-cf7a6c988dc9 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.16.0 // indirect
	github.com/prometheus/client_model v0.4.0 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.11.0 // indirect
	github.com/pyroscope-io/godeltaprof v0.1.0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/rootless-containers/rootlesskit v1.1.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stefanberger/go-pkcs11uri v0.0.0-20201008174630-78d3cae3a980 // indirect
	github.com/tidwall/gjson v1.16.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/uber/jaeger-client-go v2.22.1+incompatible // indirect
	github.com/uber/jaeger-lib v2.2.0+incompatible // indirect
	github.com/weaveworks/promrus v1.2.0 // indirect
	go.mozilla.org/pkcs7 v0.0.0-20200128120323-432b2356ecb1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel v1.16.0 // indirect
	go.opentelemetry.io/otel/metric v1.16.0 // indirect
	go.opentelemetry.io/otel/trace v1.16.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/crypto v0.13.0 // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/oauth2 v0.8.0 // indirect
	golang.org/x/term v0.12.0 // indirect
	golang.org/x/text v0.13.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20230717213848-3f92550aa753 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230717213848-3f92550aa753 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.90.1 // indirect
	k8s.io/kube-openapi v0.0.0-20221012153701-172d655c2280 // indirect
	k8s.io/utils v0.0.0-20230220204549-a5ecb0141aa5 // indirect
	lukechampine.com/blake3 v1.1.7 // indirect
	sigs.k8s.io/json v0.0.0-20220713155537-f223a00ba0e2 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

// Do not upgrade until https://github.com/fluent/fluent-logger-golang/issues/80 is fixed
replace github.com/fluent/fluent-logger-golang => github.com/fluent/fluent-logger-golang v1.2.1

replace github.com/Yeatesss/container-software v0.0.0-20230825021639-6ddb682d79c2 => ../container-software

replace github.com/fsouza/go-dockerclient v1.3.0 => ../go-dockerclient
