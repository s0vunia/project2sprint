package orchestratorgrpc

import (
	"context"
	"errors"
	orchv1 "github.com/s0vunia/protos/gen/go/orchestrator"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"myproject/internal/config"
	"myproject/internal/models"
	"myproject/internal/repositories"
	"myproject/internal/services/orchestrator"
	"myproject/internal/services/orchestrator/utils"
	"strconv"
)

type serverAPI struct {
	orchv1.UnimplementedOrchestratorServer
	orchestrator        orchestrator.IOrchestrator
	calculationTimeouts config.CalculationTimeoutsConfig
}

func Register(gRPCServer *grpc.Server, orchestrator orchestrator.IOrchestrator, timeoutsConfig config.CalculationTimeoutsConfig) {
	orchv1.RegisterOrchestratorServer(gRPCServer, &serverAPI{orchestrator: orchestrator, calculationTimeouts: timeoutsConfig})
}

func (s *serverAPI) CreateExpression(
	ctx context.Context,
	in *orchv1.CreateExpressionRequest,
) (*orchv1.CreateExpressionResponse, error) {
	if in.Expression == "" {

		return nil, status.Error(codes.InvalidArgument, "expression is required")
	}
	if in.IdempotencyKey == "" {

		return nil, status.Error(codes.InvalidArgument, "idempotencyKey is required")
	}

	if !orchestratorutils.ValidateExpression(in.Expression) {
		return nil, status.Error(codes.InvalidArgument, "invalid expression")
	}
	userID := ctx.Value("userID").(float64)
	userIdStr := strconv.Itoa(int(userID))
	var expressionId string
	expressionByKey, err := s.orchestrator.GetExpressionByKey(ctx, in.IdempotencyKey, userIdStr)
	if expressionByKey != nil {
		if err != nil {
			log.Error(err)
			return nil, status.Error(codes.Internal, "failed to create expression")
		}
		expressionId = expressionByKey.Id
	} else {
		err, expressionId = s.orchestrator.CreateExpression(ctx, in.Expression, in.IdempotencyKey, userIdStr)
		if err != nil {
			log.Error(err)
			return nil, status.Error(codes.Internal, "failed to create expression")
		}
	}
	return &orchv1.CreateExpressionResponse{ExpressionId: expressionId}, nil
}

func (s *serverAPI) GetExpression(
	ctx context.Context,
	in *orchv1.GetExpressionRequest,
) (*orchv1.GetExpressionResponse, error) {
	if in.ExpressionId == "" {

		return nil, status.Error(codes.InvalidArgument, "expressionId is required")
	}

	userID := ctx.Value("userID").(float64)
	userIdStr := strconv.Itoa(int(userID))
	expression, err := s.orchestrator.GetExpression(ctx, in.ExpressionId, userIdStr)
	if err != nil {
		if errors.Is(err, repositories.ErrExpressionNotFound) {
			return nil, status.Error(codes.NotFound, "expression not found")
		}
		log.Error(err)
		return nil, status.Error(codes.Internal, "failed to get expression")
	}

	return s.ExpressionModelToGetExpressionResponse(expression), nil
}

func (s *serverAPI) ExpressionModelToGetExpressionResponse(expression *models.Expression) *orchv1.GetExpressionResponse {
	return &orchv1.GetExpressionResponse{
		Result:         float32(expression.Result),
		ExpressionId:   expression.Id,
		IdempotencyKey: expression.IdempotencyKey,
		Value:          expression.Value,
		State:          string(expression.State),
	}
}

func (s *serverAPI) GetExpressions(
	ctx context.Context,
	in *orchv1.GetExpressionsRequest,
) (*orchv1.GetExpressionsResponse, error) {
	userID := ctx.Value("userID").(float64)
	userIdStr := strconv.Itoa(int(userID))

	expressions, err := s.orchestrator.GetExpressions(ctx, userIdStr)
	if err != nil {
		log.Error(err)
		return nil, status.Error(codes.Internal, "failed to get expressions")
	}
	var listOfExpression []*orchv1.GetExpressionResponse
	for _, expression := range expressions {
		listOfExpression = append(listOfExpression, s.ExpressionModelToGetExpressionResponse(expression))
	}
	return &orchv1.GetExpressionsResponse{ListOfExpressions: listOfExpression}, nil
}

func (s *serverAPI) GetAgents(
	ctx context.Context,
	in *orchv1.GetAgentsRequest,
) (*orchv1.GetAgentsResponse, error) {
	agents, err := s.orchestrator.GetAgents()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get agents")
	}
	var listOfAgents []*orchv1.GetAgentResponse
	for _, agent := range agents {
		listOfAgents = append(listOfAgents, s.AgentModelToGetAgentResponse(agent))
	}
	return &orchv1.GetAgentsResponse{ListOfAgents: listOfAgents}, nil
}

func (s *serverAPI) AgentModelToGetAgentResponse(agent *models.Agent) *orchv1.GetAgentResponse {
	return &orchv1.GetAgentResponse{
		Id:        agent.Id,
		Heartbeat: float64(agent.Heartbeat),
	}
}

func (s *serverAPI) GetOperators(
	ctx context.Context,
	in *orchv1.GetOperatorsRequest,
) (*orchv1.GetOperatorsResponse, error) {
	operators := orchestratorutils.GetOperators(s.calculationTimeouts)
	var listOfOperators []*orchv1.GetOperatorResponse
	for _, operator := range operators {
		listOfOperators = append(listOfOperators, s.OperatorModelToGetOperatorResponse(operator))
	}
	return &orchv1.GetOperatorsResponse{ListOfOperators: listOfOperators}, nil
}

func (s *serverAPI) OperatorModelToGetOperatorResponse(operator *models.Operator) *orchv1.GetOperatorResponse {
	return &orchv1.GetOperatorResponse{
		Op:      operator.Op,
		Timeout: int64(operator.Timeout),
	}
}
