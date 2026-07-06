package adapters

import (
	"context"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	"agent-compose/pkg/llms"
	"agent-compose/pkg/llms/runtimefacade"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sessions"
	"agent-compose/pkg/storage/configstore"
	"agent-compose/pkg/storage/sessionstore"
)

type SessionDriver struct {
	Config   *appconfig.Config
	Store    *sessionstore.Store
	ConfigDB *configstore.ConfigStore
	Runtimes RuntimeProvider
}

func NewSessionDriver(config *appconfig.Config, store *sessionstore.Store, configDB *configstore.ConfigStore, runtimes RuntimeProvider) *SessionDriver {
	return &SessionDriver{Config: config, Store: store, ConfigDB: configDB, Runtimes: runtimes}
}

func (d *SessionDriver) StartSessionVM(ctx context.Context, session *domain.Session) error {
	ctx, cancel := context.WithTimeout(ctx, d.Config.SessionStartTimeout)
	defer cancel()

	driver, err := driverpkg.ResolveSessionRuntimeDriver(session.Summary.Driver, d.Config.RuntimeDriver)
	if err != nil {
		return err
	}
	runtime, err := d.Runtimes.ForDriver(driver)
	if err != nil {
		return err
	}

	vmState, err := d.Store.GetVMState(session.Summary.ID)
	if err != nil {
		return err
	}
	proxyState, err := d.Store.GetProxyState(session.Summary.ID)
	if err != nil {
		return err
	}
	vmState.Driver = driver
	vmState.Mode = driver
	vmState.BoxName = firstNonEmpty(vmState.BoxName, session.Summary.RuntimeRef)
	vmState.RuntimeHome = firstNonEmpty(vmState.RuntimeHome, driverpkg.RuntimeHomeForDriver(d.Config, driver))
	if err := d.prepareSessionStart(ctx, driver, session, &vmState); err != nil {
		vmState.LastError = err.Error()
		_ = d.Store.SaveVMState(session.Summary.ID, vmState)
		return err
	}

	info, err := runtime.EnsureSession(ctx, session, vmState, proxyState)
	if err != nil {
		vmState.LastError = err.Error()
		vmState.StoppedAt = time.Time{}
		_ = d.Store.SaveVMState(session.Summary.ID, vmState)
		return err
	}

	return d.saveSessionStartInfo(session, vmState, proxyState, info)
}

func (d *SessionDriver) saveSessionStartInfo(session *domain.Session, vmState domain.VMState, proxyState domain.ProxyState, info domain.SessionVMInfo) error {
	vmState, proxyState = sessions.ApplySessionStartInfo(vmState, proxyState, info, time.Now())
	if err := d.Store.SaveVMState(session.Summary.ID, vmState); err != nil {
		return err
	}
	return d.Store.SaveProxyState(session.Summary.ID, proxyState)
}

func (d *SessionDriver) StopSessionVM(ctx context.Context, session *domain.Session) error {
	driver, err := driverpkg.ResolveSessionRuntimeDriver(session.Summary.Driver, d.Config.RuntimeDriver)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, driverpkg.SessionStopContextTimeout(driver, d.Config.SessionStopTimeout))
	defer cancel()

	runtime, err := d.Runtimes.ForDriver(driver)
	if err != nil {
		return err
	}

	vmState, err := d.Store.GetVMState(session.Summary.ID)
	if err != nil {
		return err
	}
	missing, err := runtime.StopSession(ctx, session, vmState)
	if err != nil {
		vmState.LastError = err.Error()
		_ = d.Store.SaveVMState(session.Summary.ID, vmState)
		return err
	}

	vmState.StoppedAt = time.Now().UTC()
	vmState.LastError = ""
	if missing {
		vmState.BoxID = ""
	}
	if d.ConfigDB != nil {
		if err := d.ConfigDB.RevokeLLMFacadeTokensForSession(ctx, session.Summary.ID); err != nil {
			return err
		}
	}
	return d.Store.SaveVMState(session.Summary.ID, vmState)
}

func (d *SessionDriver) prepareSessionStart(ctx context.Context, driver string, session *domain.Session, vmState *domain.VMState) error {
	prepared, err := driverpkg.PrepareSessionStart(ctx, d.Config, driver, execution.ToDriverSession(session), execution.ToDriverVMState(*vmState))
	if err != nil {
		return err
	}
	managedEnv, err := runtimefacade.EnsureSessionLLMFacadeConfig(ctx, d.Config, d.ConfigDB, session, "codex", "", "session", "")
	if err != nil {
		return err
	}
	if len(managedEnv) > 0 {
		session.RuntimeEnvItems = llms.EnvItemsFromMap(managedEnv, false)
	}
	*vmState = execution.FromDriverVMState(prepared)
	return nil
}
