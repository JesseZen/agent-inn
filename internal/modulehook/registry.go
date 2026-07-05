package modulehook

import (
	"fmt"
	"sort"

	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

type hookDefinition struct {
	name    string
	support appruntime.ModuleProtocolSupport
	build   func(module.ModuleConfig, BuildDependencies) (Hook, error)
}

var lifecycleHookDefinitions = []hookDefinition{
	{
		name: ConfigPatchName,
		support: appruntime.ModuleProtocolSupport{
			Protocols: []appruntime.ProtocolKind{
				appruntime.ProtocolResponses,
				appruntime.ProtocolChatCompletions,
				appruntime.ProtocolClaudeCode,
			},
		},
		build: func(cfg module.ModuleConfig, deps BuildDependencies) (Hook, error) {
			return NewConfigPatch(cfg, deps), nil
		},
	},
}

func Support(external map[string]ExternalHookRuntime) map[string]appruntime.ModuleProtocolSupport {
	support := make(map[string]appruntime.ModuleProtocolSupport, len(lifecycleHookDefinitions)+len(external))
	for _, definition := range lifecycleHookDefinitions {
		support[definition.name] = cloneProtocolSupport(definition.support)
	}
	for name, runtime := range external {
		support[name] = cloneProtocolSupport(runtime.ProtocolSupport)
	}
	return support
}

func Names() []string {
	names := make([]string, len(lifecycleHookDefinitions))
	for i, definition := range lifecycleHookDefinitions {
		names[i] = definition.name
	}
	return names
}

func IsLifecycleHook(name string) bool {
	for _, definition := range lifecycleHookDefinitions {
		if definition.name == name {
			return true
		}
	}
	return false
}

func Build(configs map[string]module.ModuleConfig, deps BuildDependencies) ([]Hook, error) {
	for name := range configs {
		_, external := deps.ExternalHooks[name]
		if !IsLifecycleHook(name) && !external {
			return nil, fmt.Errorf("unknown lifecycle hook %q", name)
		}
	}
	hooks := []Hook{}
	for _, definition := range lifecycleHookDefinitions {
		cfg := module.CloneModuleConfig(configs[definition.name])
		if !cfg.Enabled {
			continue
		}
		hook, err := definition.build(cfg, deps)
		if err != nil {
			return nil, err
		}
		if hook != nil {
			hooks = append(hooks, hook)
		}
	}
	externalNames := make([]string, 0, len(deps.ExternalHooks))
	for name := range deps.ExternalHooks {
		if IsLifecycleHook(name) {
			return nil, fmt.Errorf("external lifecycle hook %q conflicts with builtin hook", name)
		}
		if configs[name].Enabled {
			externalNames = append(externalNames, name)
		}
	}
	sort.Strings(externalNames)
	for _, name := range externalNames {
		hooks = append(hooks, NewExternalHook(name, module.CloneModuleConfig(configs[name]), deps.ExternalHooks[name], deps))
	}
	return hooks, nil
}

func cloneProtocolSupport(s appruntime.ModuleProtocolSupport) appruntime.ModuleProtocolSupport {
	out := appruntime.ModuleProtocolSupport{}
	if s.Protocols != nil {
		out.Protocols = append([]appruntime.ProtocolKind(nil), s.Protocols...)
	}
	if s.Capabilities != nil {
		out.Capabilities = append([]appruntime.ProtocolCapability(nil), s.Capabilities...)
	}
	return out
}
