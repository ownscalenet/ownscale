module github.com/ownscalenet/ownscale

go 1.27.0

require (
	github.com/caarlos0/env/v11 v11.4.1
	github.com/creachadair/jrpc2 v1.3.5
	github.com/descope/virtualwebauthn v1.0.5
	github.com/glebarez/go-sqlite v1.22.0
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/go-webauthn/webauthn v0.18.1
	github.com/gorilla/sessions v1.4.0
	github.com/juanfont/headscale v0.29.4
	github.com/markbates/goth v1.82.0
	github.com/peterbourgon/ff/v4 v4.0.0-beta.1
	github.com/pressly/goose/v3 v3.27.3
	github.com/rs/xid v1.6.0
	github.com/rs/zerolog v1.35.1
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
	github.com/zitadel/oidc/v3 v3.49.2
	golang.org/x/crypto v0.57.0
	golang.org/x/sync v0.23.0
	gopkg.in/yaml.v3 v3.0.1
	tailscale.com v1.103.0-pre.0.20260819232550-0e84b4a3a0a0
)

replace github.com/juanfont/headscale => github.com/ownscalenet/headscale v0.29.4-4

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/ClickHouse/ch-go v0.73.0 // indirect
	github.com/ClickHouse/clickhouse-go/v2 v2.47.0 // indirect
	github.com/akutz/memconn v0.1.0 // indirect
	github.com/alexbrainman/sspi v0.0.0-20250919150558-7d374ff0d59e // indirect
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/arl/statsviz v0.8.0 // indirect
	github.com/axiomhq/hyperloglog v0.2.6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chasefleming/elem-go v0.36.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/coreos/go-oidc/v3 v3.21.0 // indirect
	github.com/creachadair/mds v0.31.0 // indirect
	github.com/creachadair/msync v0.10.1 // indirect
	github.com/danielgtaylor/huma/v2 v2.39.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dblohm7/wingoes v0.0.0-20250822163801-6d8e6105c62d // indirect
	github.com/dgryski/go-metro v0.0.0-20250106013310-edb8663e5e33 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/elastic/go-sysinfo v1.15.5 // indirect
	github.com/elastic/go-windows v1.0.2 // indirect
	github.com/felixge/fgprof v0.9.5 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.3 // indirect
	github.com/gaissmai/bart v0.26.1 // indirect
	github.com/glebarez/sqlite v1.11.0 // indirect
	github.com/go-chi/chi/v5 v5.3.2 // indirect
	github.com/go-chi/metrics v0.1.1 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-gormigrate/gormigrate/v2 v2.1.7 // indirect
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.3.1 // indirect
	github.com/go4org/hashtriemap v0.0.0-20251130024219-545ba229f689 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/pprof v0.0.0-20260802141513-ef3492d7dac3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.8-0.20260903152128-9c13c57de0be // indirect
	github.com/hdevalence/ed25519consensus v0.2.0 // indirect
	github.com/huin/goupnp v1.3.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.11.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/jsimonetti/rtnetlink v1.4.2 // indirect
	github.com/kamstrup/intmap v0.5.2 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/markbates/going v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mdlayher/netlink v1.8.0 // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/mfridman/xflag v0.1.0 // indirect
	github.com/microsoft/go-mssqldb v1.10.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/muhlemmer/gu v0.3.1 // indirect
	github.com/muhlemmer/httpforwarded v0.1.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/oapi-codegen/runtime v1.7.0 // indirect
	github.com/paulmach/orb v0.13.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/petermattis/goid v0.0.0-20260330135022-df67b199bc81 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pires/go-proxyproto v0.9.2 // indirect
	github.com/pkg/profile v1.7.0 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.71.0 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/puzpuzpuz/xsync/v4 v4.5.0 // indirect
	github.com/realclientip/realclientip-go v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/safchain/ethtool v0.7.0 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/sasha-s/go-deadlock v0.3.9 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tailscale/certstore v0.1.1-0.20260409135935-3638fb84b77d // indirect
	github.com/tailscale/go-winio v0.0.0-20231025203758-c4f33415bf55 // indirect
	github.com/tailscale/peercred v0.0.0-20250107143737-35a0c7bd7edc // indirect
	github.com/tailscale/setec v0.0.0-20260310221408-dcd97e42f251 // indirect
	github.com/tailscale/squibble v0.0.0-20260831233316-9a4c8961e6f5 // indirect
	github.com/tailscale/tailsql v0.0.0-20260831145903-795321974573 // indirect
	github.com/tailscale/web-client-prebuilt v0.0.0-20251127225136-f19339b67368 // indirect
	github.com/tailscale/wireguard-go v0.0.0-20260813211458-aa425f19bcbf // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/tursodatabase/libsql-client-go v0.0.0-20260528064733-9d5d30a29a60 // indirect
	github.com/vertica/vertica-sql-go v1.3.8 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/ydb-platform/ydb-go-genproto v0.0.0-20260428144813-1c07baab7f7b // indirect
	github.com/ydb-platform/ydb-go-sdk/v3 v3.144.6 // indirect
	github.com/zitadel/schema v1.3.2 // indirect
	github.com/ziutek/mymysql v1.5.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go4.org/mem v0.0.0-20240501181205-ae6ca9944745 // indirect
	go4.org/netipx v0.0.0-20260823151212-3075585bcbeb // indirect
	golang.org/x/exp v0.0.0-20260718201538-764159d718ef // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	golang.zx2c4.com/wireguard/windows v0.5.3 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gorm.io/driver/postgres v1.6.3 // indirect
	gorm.io/gorm v1.31.2 // indirect
	gvisor.dev/gvisor v0.0.0-20260224225140-573d5e7127a8 // indirect
	howett.net/plist v1.0.1 // indirect
	modernc.org/libc v1.75.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.59.0 // indirect
)

tool github.com/pressly/goose/v3/cmd/goose
