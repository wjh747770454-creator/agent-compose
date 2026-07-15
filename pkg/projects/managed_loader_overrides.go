package projects

import (
	"fmt"
	"strings"

	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

// applyManagedLoaderOverrideBuilds adopts legacy loader identities at the
// project artifact boundary. Ordinary project specs never populate overrides.
func applyManagedLoaderOverrideBuilds(project domain.ProjectRecord, revision int64, builds []SchedulerBuild, overrides map[string]domain.Loader) ([]SchedulerBuild, error) {
	if len(overrides) == 0 {
		return builds, nil
	}
	for index := range builds {
		override, ok := overrides[builds[index].Scheduler.AgentName]
		if !ok {
			continue
		}
		loaderID := strings.TrimSpace(override.Summary.ID)
		if loaderID == "" {
			return nil, fmt.Errorf("legacy loader for agent %s has no id", builds[index].Scheduler.AgentName)
		}

		loader := loaders.CloneLoader(override)
		loader.Volumes = append([]domain.VolumeMountSpec(nil), override.Volumes...)
		loader.Summary.CapsetIDs = append([]string(nil), override.Summary.CapsetIDs...)
		loader.Summary.ManagedProjectID = project.ID
		loader.Summary.ManagedRevision = revision
		loader.Summary.ManagedAgentName = builds[index].Scheduler.AgentName
		loader.Summary.ManagedSchedulerID = builds[index].Scheduler.SchedulerID

		builds[index].Scheduler.ManagedLoaderID = loaderID
		builds[index].Scheduler.Enabled = loader.Summary.Enabled
		builds[index].Scheduler.TriggerCount = len(loader.Triggers)
		builds[index].Loader = loader
		builds[index].ValidationTriggers = append([]domain.LoaderTrigger(nil), loader.Triggers...)
	}
	return builds, nil
}

func applyManagedLoaderOverrides(project domain.ProjectRecord, revision int64, schedulers []domain.ProjectSchedulerRecord, managedLoaders []domain.Loader, overrides map[string]domain.Loader) ([]domain.ProjectSchedulerRecord, []domain.Loader, error) {
	if len(overrides) == 0 {
		return schedulers, managedLoaders, nil
	}
	if len(schedulers) != len(managedLoaders) {
		return nil, nil, fmt.Errorf("project scheduler and managed loader counts differ")
	}
	builds := make([]SchedulerBuild, 0, len(schedulers))
	for index := range schedulers {
		builds = append(builds, SchedulerBuild{Scheduler: schedulers[index], Loader: managedLoaders[index]})
	}
	builds, err := applyManagedLoaderOverrideBuilds(project, revision, builds, overrides)
	if err != nil {
		return nil, nil, err
	}
	return SchedulerRecords(builds), SchedulerLoaders(builds), nil
}

func syncProjectAgentSchedulerState(agents []domain.ProjectAgentRecord, schedulers []domain.ProjectSchedulerRecord) {
	enabledByAgent := make(map[string]bool, len(schedulers))
	for _, scheduler := range schedulers {
		enabledByAgent[scheduler.AgentName] = scheduler.Enabled
	}
	for index := range agents {
		if enabled, ok := enabledByAgent[agents[index].AgentName]; ok {
			agents[index].SchedulerEnabled = enabled
		}
	}
}
