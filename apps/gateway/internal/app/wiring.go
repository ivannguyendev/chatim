package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/api"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/limit"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/corecall"
	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const metricsSubsystem = "gateway"

func wire(cfg config.Config, log *slog.Logger, deps Deps) (*node, error) {
	verifier, err := jwtverify.LoadFile(cfg.JWTKeysFile, jwtverify.Config{Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience, Leeway: cfg.JWTLeeway})
	if err != nil {
		return nil, fmt.Errorf("load jwt key set: %w", err)
	}
	conn, err := grpcclient.NewStatic(cfg.CoreAddrs, grpcclient.Options{
		Creds:       insecure.NewCredentials(),
		DialOptions: append([]grpc.DialOption{grpc.WithIdleTimeout(0)}, deps.DialOptions...),
	})
	if err != nil {
		return nil, err
	}
	n, err := wireServers(cfg, log, verifier, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return n, nil
}

func wireServers(cfg config.Config, log *slog.Logger, verifier *jwtverify.Verifier, conn *grpc.ClientConn) (*node, error) {
	core := corecall.Conn{Core: chatimv1.NewCoreServiceClient(conn), Mention: chatimv1.NewMentionServiceClient(conn)}
	fwd, err := corecall.New(corecall.Options{
		Methods:  corecall.Request(),
		Deadline: cfg.RequestDeadline,
		Route:    func(corecall.Command, proto.Message) (corecall.Conn, error) { return core, nil },
	})
	if err != nil {
		return nil, err
	}
	gate, err := limit.NewGate(cfg.InflightMax)
	if err != nil {
		return nil, err
	}
	users, err := limit.NewUsers(limit.UserConfig{Rate: cfg.UserRate, Burst: cfg.UserBurst, Inflight: cfg.UserInflight, Capacity: cfg.UserLimiters})
	if err != nil {
		return nil, err
	}
	handler, err := api.New(api.Options{
		Forwarder: fwd, Verifier: verifier, Gate: gate, Users: users,
		BodyMax: cfg.BodyMax, CORSOrigins: cfg.CORSOrigins, Log: log,
	})
	if err != nil {
		return nil, err
	}
	metricsHandler, err := metrics.Handler(metricsSubsystem, metricSources(handler, fwd))
	if err != nil {
		return nil, err
	}
	adm := admin.New(admin.Config{Addr: cfg.AdminAddr, Metrics: metricsHandler}, log)
	return &node{
		cfg:   cfg,
		log:   log,
		conn:  conn,
		admin: adm,
		http:  newHTTPServer(cfg, handler, log),
		ready: &readiness{admin: adm, log: log},
	}, nil
}

func metricSources(handler *api.Handler, fwd *corecall.Forwarder) []metrics.Source {
	out := slices.Concat(handler.Sources(), fwd.CommandSources())
	for _, latency := range fwd.CoreCallLatency() {
		out = append(out, metrics.Source{
			Name:      "core_call_seconds",
			Help:      "Core call latency of client commands, in seconds.",
			Labels:    map[string]string{"command": latency.Command},
			Histogram: latency.Read,
		})
	}
	return out
}

func newHTTPServer(cfg config.Config, handler http.Handler, log *slog.Logger) *http.Server {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		Protocols:         &protocols,
		HTTP2:             &http.HTTP2Config{MaxConcurrentStreams: cfg.H2MaxStreams},
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
