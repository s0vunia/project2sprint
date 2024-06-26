package gRPCorchestrator

import (
	"context"
	"github.com/s0vunia/protos/gen/go/orchestrator"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log"
	"testing"
)

func TestGRPCServiceUnauthenticated(t *testing.T) {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	conn, err := grpc.Dial("localhost:44044", grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()
	client := orchestrator.NewOrchestratorClient(conn)

	// no authenticated (not use token)
	ctx := context.WithValue(context.Background(), "userID", 1)
	response, err := client.CreateExpression(ctx, &orchestrator.CreateExpressionRequest{
		IdempotencyKey: "abc",
		Expression:     "2+2*2",
	})

	log.Printf("%v", response)
	assert.NotNil(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, st.Code(), codes.Unauthenticated)

}
