package core

import (
	"context"
	"fmt"

	"awscope/internal/actions"
	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/aws"
	"awscope/internal/graph"
	"awscope/internal/store"

	"github.com/google/uuid"
)

type ActionRunResult struct {
	ActionID    string
	ActionRunID string
	Status      string
}

func RunAction(ctx context.Context, st *store.Store, actionID string, key graph.ResourceKey, profileName string) (ActionRunResult, error) {
	a, ok := actionsRegistry.Get(actionID)
	if !ok {
		return ActionRunResult{}, fmt.Errorf("unknown action %q (known: %v)", actionID, actionsRegistry.ListIDs())
	}

	node, err := st.GetResource(ctx, key)
	if err != nil {
		return ActionRunResult{}, err
	}
	if !a.Applicable(node) {
		return ActionRunResult{}, fmt.Errorf("action %q not applicable to resource type %q service %q", actionID, node.Type, node.Service)
	}

	partition, accountID, region, _, _, err := graph.ParseResourceKey(key)
	if err != nil {
		return ActionRunResult{}, err
	}

	loader := aws.NewLoader()
	cfg, id, err := loader.Load(ctx, profileName, region)
	if err != nil {
		return ActionRunResult{}, err
	}
	if err := aws.RequireIdentity(id); err != nil {
		return ActionRunResult{}, err
	}
	if accountID != "" && id.AccountID != "" && accountID != id.AccountID {
		return ActionRunResult{}, fmt.Errorf("resource_key account %s does not match current identity account %s", accountID, id.AccountID)
	}

	runID := uuid.NewString()
	_ = st.StartActionRun(ctx, store.ActionRunStart{
		ActionRunID: runID,
		ProfileName: profileName,
		AccountID:   id.AccountID,
		Region:      region,
		ResourceKey: string(key),
		ActionID:    actionID,
		Input:       map[string]any{},
	})

	res, execErr := a.Execute(ctx, actions.ExecContext{
		Store:       st,
		Loader:      loader,
		AWSConfig:   cfg,
		Profile:     profileName,
		AccountID:   id.AccountID,
		Partition:   partition,
		Region:      region,
		ActionRunID: runID,
	}, node)

	status := "SUCCEEDED"
	result := res.Data
	if result == nil {
		result = map[string]any{}
	}
	if execErr != nil {
		status = "FAILED"
		result["error"] = execErr.Error()
	}
	_ = st.FinishActionRun(ctx, store.ActionRunFinish{
		ActionRunID: runID,
		Status:      status,
		Result:      result,
	})

	if execErr != nil {
		return ActionRunResult{ActionID: actionID, ActionRunID: runID, Status: status}, execErr
	}
	return ActionRunResult{ActionID: actionID, ActionRunID: runID, Status: status}, nil
}
