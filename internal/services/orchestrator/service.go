package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"log"
	"myproject/internal/config"
	"myproject/internal/models"
	"myproject/internal/repositories/agent"
	"myproject/internal/repositories/expression"
	"myproject/internal/repositories/queue"
	"myproject/internal/repositories/subExpression"
	"time"
)

type IOrchestrator interface {
	CreateExpression(ctx context.Context, expression, idempotencyKey string) (error, string)
	GetExpressions(ctx context.Context) ([]*models.Expression, error)
	GetSubExpressions(ctx context.Context) ([]*models.SubExpression, error)
	GetExpression(ctx context.Context, id string) (*models.Expression, error)
	GetExpressionByKey(ctx context.Context, key string) (*models.Expression, error)
	UpdateExpressionState(ctx context.Context, key string, state models.ExpressionState) error
	// ReceiveHeartbeats принимает heartbeats из очереди от агента
	ReceiveHeartbeats()
	// ReceiveCalculations принимает подсчитанные subexpression из очереди от агента
	ReceiveCalculations(ctx context.Context)
	CreateAgentIfNotExists(id string)
	GetAgents() ([]*models.Agent, error)
	// SendSubExpression отправляет subexpressions в очередь, которые могут подсчитаться (являются независимыми от ответов других subexpressions)
	SendSubExpression()
	// ReceiveRPCTasks принимает ответы от агента о том, какой subexpression он взял на обработку
	ReceiveRPCTasks(ctx context.Context)
	// RetrySubExpressions переназначает неподсчитанные subexpressions умершего агента на другого
	RetrySubExpressions(ctx context.Context)
}

type Orchestrator struct {
	expressionRepository    expression.Repository
	subExpressionRepository subExpression.Repository
	agentRepository         agent.Repository
	queueRepository         queue.Queue
}

func NewOrchestrator(ctx context.Context, expressionRepo expression.Repository,
	subExpressionRepo subExpression.Repository,
	queueRepo queue.Queue,
	agentRepo agent.Repository) *Orchestrator {
	orch := &Orchestrator{
		expressionRepository:    expressionRepo,
		subExpressionRepository: subExpressionRepo,
		agentRepository:         agentRepo,
		queueRepository:         queueRepo,
	}
	go orch.SendSubExpression()
	go orch.ReceiveHeartbeats()
	go orch.ReceiveCalculations(ctx)
	go orch.ReceiveRPCTasks(ctx)
	go orch.RetrySubExpressions(ctx)
	return orch
}

func (o *Orchestrator) CreateExpression(ctx context.Context, expression, idempotencyKey string) (error, string) {
	createdExpression, err := o.expressionRepository.CreateExpression(ctx, expression, idempotencyKey)
	if err != nil {
		return err, ""
	}
	_, err = splitToSubtasks(ctx, createdExpression, o.subExpressionRepository)
	if err != nil {
		exprId, _ := uuid.Parse(createdExpression.Id)
		o.subExpressionRepository.DeleteSubExpressionsByExpressionId(ctx, exprId)
		o.expressionRepository.DeleteExpressionById(ctx, exprId)
		return fmt.Errorf("error split to subtasks: %e", err), ""
	}
	return nil, createdExpression.Id
}

func (o *Orchestrator) GetExpressions(ctx context.Context) ([]*models.Expression, error) {
	return o.expressionRepository.GetExpressions(ctx)
}

func (o *Orchestrator) GetSubExpressions(ctx context.Context) ([]*models.SubExpression, error) {
	return o.subExpressionRepository.GetSubExpressionsList(ctx)
}

func (o *Orchestrator) GetExpression(ctx context.Context, id string) (*models.Expression, error) {
	return o.expressionRepository.GetExpressionById(ctx, id)
}

func (o *Orchestrator) GetExpressionByKey(ctx context.Context, key string) (*models.Expression, error) {
	return o.expressionRepository.GetExpressionByKey(ctx, key)
}

func (o *Orchestrator) UpdateExpressionState(ctx context.Context, key string, state models.ExpressionState) error {
	return o.expressionRepository.UpdateState(ctx, key, state)
}

func (o *Orchestrator) ReceiveHeartbeats() {
	err := o.queueRepository.Connect(config.NameQueueWithHeartbeats)
	if err != nil {
		log.Fatalf("Failed to connect to queue: %v", err)
	}
	defer o.queueRepository.Close()

	heartbeats, err := o.queueRepository.Consume()
	if err != nil {
		log.Printf("Failed to consume tasks from queue: %v", err)
	}
	for heartbeat := range heartbeats {
		agent := models.Agent{}
		err = json.Unmarshal(heartbeat, &agent)
		if err != nil {
			log.Printf("Failed to decode agent: %v", err)
			continue
		}
		o.CreateAgentIfNotExists(agent.Id)
	}
}

func (o *Orchestrator) ReceiveCalculations(ctx context.Context) {
	err := o.queueRepository.Connect(config.NameQueueWithFinishedTasks)
	if err != nil {
		log.Fatalf("Failed to connect to queue: %v", err)
	}
	defer o.queueRepository.Close()

	finishedTasks, err := o.queueRepository.Consume()
	if err != nil {
		log.Printf("Failed to consume tasks from queue: %v", err)
	}
	for task := range finishedTasks {
		expressionStruct := &models.SubExpression{}
		err = json.Unmarshal(task, expressionStruct)
		if err != nil {
			log.Printf("error unmarshal subexpression: %e", err)
		}
		if expressionStruct.Error {
			err = o.subExpressionRepository.DeleteSubExpressionsByExpressionId(ctx, expressionStruct.ExpressionId)
			if err != nil {
				log.Printf("error delete subexpressions: %e", err)
			}
			err = o.expressionRepository.UpdateState(ctx, expressionStruct.ExpressionId.String(), models.ExpressionState(models.Error))
			if err != nil {
				log.Printf("error update state: %e", err)
			}
			continue
		}
		err = o.subExpressionRepository.UpdateSubExpressions(ctx, expressionStruct)
		if err != nil {
			log.Printf("error update subexpression: %e", err)
		}
		if expressionStruct.IsLast {
			err = o.expressionRepository.UpdateExpressionById(ctx, expressionStruct.ExpressionId, expressionStruct.Result)
			if err != nil {
				log.Printf("error update expression: %e", err)
			}
			err = o.subExpressionRepository.DeleteSubExpressionsByExpressionId(ctx, expressionStruct.ExpressionId)
			if err != nil {
				log.Printf("error delete subexpressions: %e", err)
			}
		}
	}

}
func (o *Orchestrator) CreateAgentIfNotExists(id string) {
	_ = o.agentRepository.CreateIfNotExistsAndUpdateHeartbeat(id)
}

func (o *Orchestrator) GetAgents() ([]*models.Agent, error) {
	return o.agentRepository.GetAgents()
}

func (o *Orchestrator) SendSubExpression() {
	listener := o.subExpressionRepository.GetSubExpressions()
	for subExpr := range listener {
		err := o.queueRepository.Connect(config.NameQueueWithTasks)
		if err != nil {
			log.Printf("")
		}
		expressionJson, err := json.Marshal(subExpr)
		if err != nil {
			log.Printf("")
		}
		err = o.queueRepository.Publish(expressionJson)
		if err != nil {
			log.Printf("")
		}
		o.queueRepository.Close()
	}
}
func (o *Orchestrator) ReceiveRPCTasks(ctx context.Context) {
	err := o.queueRepository.Connect(config.NameQueueWithRPC)
	if err != nil {
		log.Fatalf("Failed to connect to queue: %v", err)
	}
	defer o.queueRepository.Close()

	rpcTasks, err := o.queueRepository.Consume()
	if err != nil {
		log.Printf("Failed to consume tasks from queue: %v", err)
	}
	for rpc := range rpcTasks {
		rpcAnswer := models.RPCAnswer{}
		err = json.Unmarshal(rpc, &rpcAnswer)
		if err != nil {
			log.Printf("Failed to decode rpc answer: %v", err)
			continue
		}
		o.subExpressionRepository.UpdateSubExpressionAgent(ctx, rpcAnswer.IdSubExpression, rpcAnswer.IdAgent)
	}
}

func (o *Orchestrator) RetrySubExpressions(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	for _ = range ticker.C {
		agents, _ := o.agentRepository.GetAgents()
		for _, agent := range agents {
			timeAgent := time.Unix(agent.Heartbeat, 0)
			// Если от агента не поступает ответа в течение config.RetrySubExpressionTimout
			if time.Now().Add(-config.RetrySubExpressionTimout).After(timeAgent) {
				agentId, _ := uuid.Parse(agent.Id)
				// получаем все невыполненные subexpression этого агента
				tempExpressions, err := o.subExpressionRepository.GetNotCalculatedSubExpressionsByAgentId(ctx, agentId)
				if err != nil {
					log.Printf("err get sub expressions by agent id %e", err)
					continue
				}
				for _, expr := range tempExpressions {
					oldId := expr.Id
					// удаляем subexpression
					err = o.subExpressionRepository.DeleteSubExpressionById(ctx, expr.Id)
					if err != nil {
						log.Printf("err delete sub expressions by agent id %e", err)
						continue
					}
					// создаем новый
					newExpr, err := o.subExpressionRepository.CreateSubExpression(ctx, expr)
					if err != nil {
						log.Printf("error create sub expressions %e", err)
						continue
					}
					// меняем у зависимых от удаленного выражения sub_expression на новый
					err = o.subExpressionRepository.ReplaceExpressionsIds(ctx, oldId, newExpr.Id)
					if err != nil {
						log.Printf("err delete sub expressions by agent id %e", err)
						continue
					}
				}
			}
		}
	}
}
