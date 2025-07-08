package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func executeAction(ctx *pulumi.Context, action Action, template VMTemplate, roleGroups RoleGroup, globalDeps map[string]interface{}, vmpassword string) error {
	actionCtx := ActionContext{
		VMs:          roleGroups.VMs,
		IPs:          roleGroups.IPs,
		GlobalDeps:   globalDeps,
		ActionConfig: action.Config,
		VMPassword:   vmpassword,
		Templates:    template,
	}

	if handler, exists := actionHandlers[action.Type]; exists {
		return handler(ctx, actionCtx)
	} else {
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}
func executeActions(ctx *pulumi.Context, templates []VMTemplate, roleGroups map[string]RoleGroup, globalDeps map[string]interface{}, vmPassword string) error {

	completedActions := make(map[string]bool)

	type ActionItem struct {
		Template VMTemplate
		Action   Action
		ID       string
	}
	var remainingActions []ActionItem
	for _, template := range templates {
		for _, action := range template.Actions {
			actionID := fmt.Sprintf("%s-%s", template.Role, action.Type)
			remainingActions = append(remainingActions, ActionItem{
				Template: template,
				Action:   action,
				ID:       actionID,
			})
		}
	}
	//fmt.Println(len(remainingActions))

	for len(remainingActions) > 0 {
		executed := false
		//	fmt.Println(len(remainingActions))

		for i, item := range remainingActions {
			canExecute := true

			for _, dep := range item.Action.DependsOn {
				depSatisfied := false
				//fmt.Println("print my work")
				if hasRoleCompleted(dep, completedActions) {
					depSatisfied = true
					ctx.Log.Info(fmt.Sprintf("Role dependency '%s' satisfied for action '%s'", dep, item.Action.Type), nil)
				} else if completedActions[dep] {
					depSatisfied = true
					ctx.Log.Info(fmt.Sprintf("Action dependency '%s' satisfied for action '%s'", dep, item.Action.Type), nil)
				} else {
					ctx.Log.Info(fmt.Sprintf("Dependency '%s' NOT satisfied for action '%s'", dep, item.Action.Type), nil)
				}

				if !depSatisfied {
					canExecute = false
					break
				}
			}
			if canExecute {
				ctx.Log.Info(fmt.Sprintf("Executing action '%s' for role '%s'", item.Action.Type, item.Template.Role), nil)

				roleGroup := roleGroups[item.Template.Role]
				err := executeAction(ctx, item.Action, item.Template, roleGroup, globalDeps, vmPassword)
				if err != nil {
					return fmt.Errorf("failed to execute action %s for role %s: %w", item.Action.Type, item.Template.Role, err)
				}
				completedActions[item.ID] = true
				remainingActions = append(remainingActions[:i], remainingActions[i+1:]...)
				executed = true
				break
			}
		}
		if !executed {
			return fmt.Errorf("dependency deadlock - remaining actions have unsatisfied dependencies")
		}

	}
	return nil
}

func hasRoleCompleted(role string, completedActions map[string]bool) bool {
	expectedActions := map[string][]string{
		"loadbalancer":   {"install-haproxy"},
		"k3s-server":     {"install-k3s-server", "get-kubeconfig"},
		"harvester-node": {"configure-ipxe-boot"},
	}
	//fmt.Printf("DEBUG: Checking if role '%s' is complete\n", role)
	if actions, exists := expectedActions[role]; exists {
		for _, action := range actions {
			actionID := fmt.Sprintf("%s-%s", role, action)
			if !completedActions[actionID] {
				return false
			}
		}
		//	fmt.Printf("DEBUG: Role '%s' IS complete\n", role)
		return true
	}
	//fmt.Printf("DEBUG: Role '%s' not found in expectedActions, returning false\n", role)
	return false

}
