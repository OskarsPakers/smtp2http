module github.com/oskarspakers/smtp2http

go 1.25.0

require (
	github.com/alash3al/go-smtpsrv v0.0.0-20220914202710-8d4ba373d159
	github.com/go-resty/resty/v2 v2.17.2
)

require (
	github.com/emersion/go-sasl v0.0.0-20231106173351-e73c9f7bad43 // indirect
	// PINNED. alash3al/go-smtpsrv is abandoned (last commit 2022-09) and
	// targets this API; go-smtp >=v0.16 removed smtp.ConnectionState and changed
	// Backend/Session signatures, which breaks the build. Do NOT `go get -u ./...`.
	// Upgrade path: drop go-smtpsrv, call emersion/go-smtp directly.
	github.com/emersion/go-smtp v0.15.0 // indirect
	github.com/miekg/dns v1.1.58 // indirect
	github.com/zaccone/spf v0.0.0-20170817004109-76747b8658d9 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)
