package grpcapp

import (
	"context"
	"fmt"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"log"
	"log/slog"
	"myproject/internal/config"
	orchestratorgrpc "myproject/internal/grpc/orchestrator"
	"myproject/internal/repositories/app"
	"myproject/internal/services/orchestrator"
	"net"

	authgrpc "myproject/internal/grpc/auth"
	authService "myproject/internal/services/auth"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	listOfRoutesJWTMiddleware = []string{
		"/orchestrator.Orchestrator/CreateExpression",
		"/orchestrator.Orchestrator/GetExpression",
		"/orchestrator.Orchestrator/GetExpressions",
		"/orchestrator.Orchestrator/GetAgents",
		"/orchestrator.Orchestrator/GetOperators",
	}
)

type App struct {
	log        *slog.Logger
	gRPCServer *grpc.Server
	port       int
}

// New creates new gRPC server app.
func New(
	log *slog.Logger,
	authService authService.IOAuth,
	orchestratorService orchestrator.IOrchestrator,
	appRepo app.Repository,
	port int,
	timeouts config.CalculationTimeoutsConfig,
) *App {
	loggingOpts := []logging.Option{
		logging.WithLogOnEvents(
			//logging.StartCall, logging.FinishCall,
			logging.PayloadReceived, logging.PayloadSent,
		),
		// Add any other option (check functions starting with logging.With).
	}

	recoveryOpts := []recovery.Option{
		recovery.WithRecoveryHandler(func(p interface{}) (err error) {
			log.Error("Recovered from panic", slog.Any("panic", p))

			return status.Errorf(codes.Internal, "internal error")
		}),
	}

	gRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		recovery.UnaryServerInterceptor(recoveryOpts...),
		logging.UnaryServerInterceptor(InterceptorLogger(log), loggingOpts...),
		selector.UnaryServerInterceptor(authgrpc.JWTMiddleware(appRepo), selector.MatchFunc(checkGrpcNameForJWT)),
	))

	authgrpc.Register(gRPCServer, authService)
	orchestratorgrpc.Register(gRPCServer, orchestratorService, timeouts)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(gRPCServer, healthServer)

	return &App{
		log:        log,
		gRPCServer: gRPCServer,
		port:       port,
	}
}

// InterceptorLogger adapts slog logger to interceptor logger.
// This code is simple enough to be copied and not imported.
func InterceptorLogger(l *slog.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		l.Log(ctx, slog.Level(lvl), msg, fields...)
	})
}

// MustRun runs gRPC server and panics if any error occurs.
func (a *App) MustRun() {
	if err := a.Run(); err != nil {
		panic(err)
	}
}

// Run runs gRPC server.
func (a *App) Run() error {
	const op = "grpcapp.Run"

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", a.port))
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	a.log.Info("grpc server started", slog.String("addr", l.Addr().String()))

	if err := a.gRPCServer.Serve(l); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

// Stop stops gRPC server.
func (a *App) Stop() {
	const op = "grpcapp.Stop"

	a.log.With(slog.String("op", op)).
		Info("stopping gRPC server", slog.Int("port", a.port))

	a.gRPCServer.GracefulStop()
}

func checkGrpcNameForJWT(ctx context.Context, callMeta interceptors.CallMeta) bool {
	fullMethName := callMeta.FullMethod()
	log.Printf(fullMethName)
	for _, name := range listOfRoutesJWTMiddleware {
		if name == fullMethName {
			return true
		}
	}
	return false
}
