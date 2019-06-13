package lifecycle

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"

	"hub/api"
	"hub/config"
	"hub/manifest"
	"hub/parameters"
	"hub/state"
	"hub/storage"
	"hub/util"
)

const HubEnvVarNameComponentName = "HUB_COMPONENT"

func Execute(request *Request) {
	isDeploy := strings.HasPrefix(request.Verb, "deploy")
	isUndeploy := strings.HasPrefix(request.Verb, "undeploy")
	isSomeComponents := len(request.Components) > 0 || request.OffsetComponent != ""

	stackManifest, componentsManifests, chosenManifestFilename, err := manifest.ParseManifest(request.ManifestFilenames)
	if err != nil {
		log.Fatalf("Unable to %s: %s", request.Verb, err)
	}

	environment, err := manifest.ParseKvList(request.EnvironmentOverrides)
	if err != nil {
		log.Fatalf("Unable to parse environment settings `%s`: %v", request.EnvironmentOverrides, err)
	}

	if config.Verbose {
		printStartBlurb(request, chosenManifestFilename, stackManifest)
	}

	stackBaseDir := util.Basedir(request.ManifestFilenames)
	componentsBaseDir := request.ComponentsBaseDir
	if componentsBaseDir == "" {
		componentsBaseDir = stackBaseDir
	}

	components := stackManifest.Components
	checkComponentsManifests(components, componentsManifests)
	// TODO check only -c / -o components sources
	checkComponentsSourcesExist(components, stackBaseDir, componentsBaseDir)
	checkLifecycleOrder(components, stackManifest.Lifecycle)
	checkLifecycleVerbs(components, componentsManifests, stackManifest.Lifecycle.Verbs, stackBaseDir, componentsBaseDir)
	checkLifecycleRequires(components, stackManifest.Lifecycle.Requires)
	checkComponentsDepends(components, stackManifest.Lifecycle.Order)
	manifest.CheckComponentsExist(components, append(request.Components, request.OffsetComponent, request.LimitComponent)...)
	optionalRequires := parseRequiresTunning(stackManifest.Lifecycle.Requires)
	provides := checkStackRequires(stackManifest.Requires, optionalRequires)
	mergePlatformProvides(provides, stackManifest.Platform.Provides)
	if config.Debug && len(provides) > 0 {
		log.Print("Requirements provided by:")
		util.PrintDeps(provides)
	}

	osEnv, err := initOsEnv(request.OsEnvironmentMode)
	if err != nil {
		log.Fatalf("Unable to parse OS environment setup: %v", err)
	}

	stateFiles, errs := storage.Check(request.StateFilenames, "state")
	if len(errs) > 0 {
		util.MaybeFatalf("Unable to check state files: %s", util.Errors2(errs...))
	}
	storage.EnsureNoLockFiles(stateFiles)

	defer util.Done()

	var stateManifest *state.StateManifest
	stateUpdater := func(interface{}) {}
	var operationLogId string
	if len(request.StateFilenames) > 0 {
		if isUndeploy || isSomeComponents { // skip state file at start of full deploy
			var err error
			stateManifest, err = state.ParseState(stateFiles)
			if err != nil {
				if err != os.ErrNotExist {
					log.Fatalf("Failed to read %v state files: %v", request.StateFilenames, err)
				}
				if isSomeComponents {
					comps := request.OffsetComponent
					if comps == "" {
						comps = strings.Join(request.Components, ", ")
					}
					util.MaybeFatalf("Component `%s` is specified but failed to read %v state file(s): %v",
						comps, request.StateFilenames, err)
				}
			}
		}
		stateUpdater = state.InitWriter(stateFiles)

		u, err := uuid.NewRandom()
		if err != nil {
			log.Fatalf("Unable to generate operation Id random v4 UUID: %v", err)
		}
		operationLogId = u.String()
	}

	deploymentIdParameterName := "hub.deploymentId"
	deploymentId := ""
	if stateManifest != nil {
		for _, p := range stateManifest.StackParameters {
			if p.Name == deploymentIdParameterName {
				deploymentId = p.Value
				break
			}
		}
	}
	if deploymentId == "" {
		u, err := uuid.NewRandom()
		if err != nil {
			log.Fatalf("Unable to generate `hub.deploymentId` random v4 UUID: %v", err)
		}
		deploymentId = u.String()
	}
	plainStackNameParameterName := "hub.stackName"
	plainStackName := util.PlainName(stackManifest.Meta.Name)
	extraExpansionValues := []manifest.Parameter{
		{Name: deploymentIdParameterName, Value: deploymentId},
		{Name: plainStackNameParameterName, Value: plainStackName},
	}

	// TODO state file has user-level parameters for undeploy operation
	// should we just go with the state values if we cannot lock all parameters properly?
	stackParameters, errs := parameters.LockParameters(
		manifest.FlattenParameters(stackManifest.Parameters, chosenManifestFilename),
		extraExpansionValues,
		func(parameter *manifest.Parameter) error {
			return AskParameter(parameter, environment,
				request.Environment, request.StackInstance, request.Application,
				isDeploy)
		})
	if len(errs) > 0 {
		log.Fatalf("Failed to lock stack parameters:\n\t%s", util.Errors("\n\t", errs...))
	}
	allOutputs := make(parameters.CapturedOutputs)
	if stateManifest != nil {
		checkStateMatch(stateManifest, stackManifest, stackParameters)
		state.MergeParsedStateParametersAndProvides(stateManifest, stackParameters, provides)
		if isUndeploy && !isSomeComponents {
			state.MergeParsedStateOutputs(stateManifest,
				"", []string{}, stackManifest.Lifecycle.Order, false,
				allOutputs)
		}
	}
	if stateManifest == nil && isDeploy {
		stateManifest = &state.StateManifest{
			Meta: state.Metadata{
				Kind: stackManifest.Kind,
				Name: stackManifest.Meta.Name,
			},
		}
	}
	addLockedParameter(stackParameters, deploymentIdParameterName, "DEPLOYMENT_ID", deploymentId)
	addLockedParameter(stackParameters, plainStackNameParameterName, "STACK_NAME", plainStackName)
	stackParametersNoLinks := parameters.ParametersWithoutLinks(stackParameters)

	order := stackManifest.Lifecycle.Order
	if stateManifest != nil {
		stateManifest.Lifecycle.Order = order
	}

	offsetGuessed := false
	if isUndeploy {
		order = util.Reverse(order)

		// on undeploy, guess which component failed to deploy and start undeploy from it
		if request.GuessComponent && stateManifest != nil && !isSomeComponents {
			for i, component := range order {
				if _, exist := stateManifest.Components[component]; exist {
					if i == 0 {
						break
					}
					if config.Verbose {
						log.Printf("State file has a state for `%[1]s` - setting `--offset %[1]s`", component)
					}
					request.OffsetComponent = component
					offsetGuessed = true
					break
				}
			}
		}
	}

	offsetComponentIndex := util.Index(order, request.OffsetComponent)
	limitComponentIndex := util.Index(order, request.LimitComponent)
	if offsetComponentIndex >= 0 && limitComponentIndex >= 0 &&
		limitComponentIndex < offsetComponentIndex && !offsetGuessed {
		log.Fatalf("Specified --limit %s (%d) is before specified --offset %s (%d) in component order",
			request.LimitComponent, limitComponentIndex, request.OffsetComponent, offsetComponentIndex)
	}

	failedComponents := make([]string, 0)

	// TODO handle ^C interrupt to update op log and stack status
	// or expiry by time and set to `interrupted`
	if stateManifest != nil {
		stateManifest = state.UpdateOperation(stateManifest, operationLogId, request.Verb, "in-progress",
			map[string]interface{}{"args": os.Args})
	}

NEXT_COMPONENT:
	for componentIndex, componentName := range order {
		if (len(request.Components) > 0 && !util.Contains(request.Components, componentName)) ||
			(offsetComponentIndex >= 0 && componentIndex < offsetComponentIndex) ||
			(limitComponentIndex >= 0 && componentIndex > limitComponentIndex) {
			if config.Debug {
				log.Printf("Skip %s", componentName)
			}
			continue
		}

		if config.Verbose {
			log.Printf("%s ***%s*** (%d/%d)", maybeTestVerb(request.Verb, request.DryRun),
				componentName, componentIndex+1, len(components))
		}

		component := manifest.ComponentRefByName(components, componentName)
		componentManifest := manifest.ComponentManifestByRef(componentsManifests, component)

		if stateManifest != nil && (componentIndex == offsetComponentIndex || len(request.Components) > 0) {
			if len(request.Components) > 0 {
				allOutputs = make(parameters.CapturedOutputs)
			}
			state.MergeParsedStateOutputs(stateManifest,
				componentName, component.Depends, stackManifest.Lifecycle.Order, isDeploy,
				allOutputs)
		}

		var updateStateComponentFailed func(string, bool)
		if stateManifest != nil {
			updateStateComponentFailed = func(msg string, final bool) {
				stateManifest = state.UpdateComponentStatus(stateManifest, componentName, "error", msg)
				stateManifest = state.UpdatePhase(stateManifest, operationLogId, componentName, "error")
				if !config.Force && !optionalComponent(&stackManifest.Lifecycle, componentName) {
					stateManifest = state.UpdateStackStatus(stateManifest, "incomplete", msg)
				}
				if final {
					stateManifest = state.UpdateOperation(stateManifest, operationLogId, request.Verb, "error", nil)
				}
				stateUpdater(stateManifest)
			}
		}

		if isDeploy && len(component.Depends) > 0 {
			failed := make([]string, 0, len(component.Depends))
			for _, dependency := range component.Depends {
				if util.Contains(failedComponents, dependency) {
					failed = append(failed, dependency)
				}
			}
			if len(failed) > 0 {
				maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName,
					fmt.Sprintf("Component `%s` failed to %s: depends on failed optional component `%s`",
						componentName, request.Verb, strings.Join(failed, ", ")),
					updateStateComponentFailed)
				failedComponents = append(failedComponents, componentName)
				continue NEXT_COMPONENT
			}
		}

		expandedComponentParameters, expansionErrs := parameters.ExpandParameters(componentName, component.Depends,
			stackParameters, allOutputs,
			manifest.FlattenParameters(componentManifest.Parameters, componentManifest.Meta.Name),
			environment)
		expandedComponentParameters = addHubProvides(expandedComponentParameters, provides)
		allParameters := parameters.MergeParameters(stackParametersNoLinks, expandedComponentParameters)
		optionalParametersFalse := calculateOptionalFalseParameters(componentName, allParameters, optionalRequires)
		if len(optionalParametersFalse) > 0 {
			log.Printf("Surprisingly, let skip `%s` due to optional parameter %v evaluated to false", componentName, optionalParametersFalse)

			continue NEXT_COMPONENT
		}
		if len(expansionErrs) > 0 {
			log.Printf("Component `%s` failed to %s", componentName, request.Verb)
			maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName,
				fmt.Sprintf("Component `%s` parameters expansion failed:\n\t%s",
					componentName, util.Errors("\n\t", expansionErrs...)),
				updateStateComponentFailed)
			failedComponents = append(failedComponents, componentName)
			continue NEXT_COMPONENT
		}

		var componentParameters parameters.LockedParameters
		if request.StrictParameters {
			componentParameters = parameters.MergeParameters(make(parameters.LockedParameters), expandedComponentParameters)
		} else {
			componentParameters = allParameters
		}

		if optionalNotProvided, err := prepareComponentRequires(provides, componentManifest, allParameters, allOutputs, optionalRequires); len(optionalNotProvided) > 0 || err != nil {
			if err != nil {
				maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName, fmt.Sprintf("%v", err), updateStateComponentFailed)
				continue NEXT_COMPONENT
			}
			log.Printf("Skip %s due to unsatisfied optional requirements %v", componentName, optionalNotProvided)
			// there will be a gap in state file but `deploy -c` will be able to find some state from
			// a preceding component
			continue NEXT_COMPONENT
		}

		if stateManifest != nil {
			status := fmt.Sprintf("%sing", request.Verb)
			if isDeploy {
				stateManifest = state.UpdateState(stateManifest, componentName,
					stackParameters, expandedComponentParameters,
					nil, allOutputs, stackManifest.Outputs,
					noEnvironmentProvides(provides),
					false)
			}
			stateManifest = state.UpdateComponentStatus(stateManifest, componentName, status, "")
			stateManifest = state.UpdateStackStatus(stateManifest, status, "")
			stateManifest = state.UpdatePhase(stateManifest, operationLogId, componentName, "in-progress")
			stateUpdater(stateManifest)
			stateUpdater("sync")
		}

		componentDir := manifest.ComponentSourceDirFromRef(component, stackBaseDir, componentsBaseDir)
		stdout, stderr, err := delegate(maybeTestVerb(request.Verb, request.DryRun),
			component, componentManifest, componentParameters,
			componentDir, osEnv, request.PipeOutputInRealtime)

		var rawOutputs parameters.RawOutputs
		if err != nil {
			if stateManifest != nil {
				stateManifest = state.AppendOperationLog(stateManifest, operationLogId,
					fmt.Sprintf("%v%s", err, formatStdoutStderr(stdout, stderr)))
			}
			maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName,
				fmt.Sprintf("Component `%s` failed to %s: %v", componentName, request.Verb, err),
				updateStateComponentFailed)
			failedComponents = append(failedComponents, componentName)
		} else if isDeploy {
			var componentOutputs parameters.CapturedOutputs
			var dynamicProvides []string
			var errs []error
			rawOutputs, componentOutputs, dynamicProvides, errs =
				captureOutputs(componentName, componentParameters, stdout, componentDir, componentManifest.Outputs)

			if len(errs) > 0 {
				log.Printf("Component `%s` failed to %s", componentName, request.Verb)
				maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName,
					fmt.Sprintf("Component `%s` outputs capture failed:\n\t%s",
						componentName, util.Errors("\n\t", errs...)),
					updateStateComponentFailed)
				failedComponents = append(failedComponents, componentName)
			}
			if len(componentOutputs) > 0 &&
				(config.Debug || (config.Verbose && len(request.Components) == 1)) {
				log.Print("Component outputs:")
				parameters.PrintCapturedOutputs(componentOutputs)
			}
			parameters.MergeOutputs(allOutputs, componentOutputs)

			if request.GitOutputs {
				if config.Verbose {
					log.Print("Checking Git status")
				}
				git := gitOutputs(componentName, componentDir, request.GitOutputsStatus)
				if config.Debug && len(git) > 0 {
					log.Print("Implicit Git outputs added:")
					parameters.PrintCapturedOutputs(git)
				}
				parameters.MergeOutputs(allOutputs, git)
			}

			componentComplexOutputs := captureProvides(component, stackBaseDir, componentsBaseDir,
				componentManifest.Provides, componentOutputs)
			if len(componentComplexOutputs) > 0 &&
				(config.Debug || (config.Verbose && len(request.Components) == 1)) {
				log.Print("Component additional outputs captured:")
				parameters.PrintCapturedOutputs(componentComplexOutputs)
			}
			parameters.MergeOutputs(allOutputs, componentComplexOutputs)

			mergeProvides(provides, componentName, append(dynamicProvides, componentManifest.Provides...), componentOutputs)
		}

		if isDeploy && stateManifest != nil {
			final := componentIndex == len(order)-1 || (len(request.Components) > 0 && request.LoadFinalState)
			stateManifest = state.UpdateState(stateManifest, componentName,
				stackParameters, expandedComponentParameters,
				rawOutputs, allOutputs, stackManifest.Outputs,
				noEnvironmentProvides(provides), final)
		}

		if err == nil && isDeploy {
			err = waitForReadyConditions(componentManifest.Lifecycle.ReadyConditions, componentParameters, allOutputs)
			if err != nil {
				log.Printf("Component `%s` failed to %s", componentName, request.Verb)
				maybeFatalIfMandatory(&stackManifest.Lifecycle, componentName,
					fmt.Sprintf("Component `%s` ready condition failed: %v", componentName, err),
					updateStateComponentFailed)
				failedComponents = append(failedComponents, componentName)
			}
		}

		if err == nil && config.Verbose {
			log.Printf("Component `%s` completed %s", componentName, request.Verb)
		}

		if stateManifest != nil {
			if !util.Contains(failedComponents, componentName) {
				stateManifest = state.UpdateComponentStatus(stateManifest, componentName, fmt.Sprintf("%sed", request.Verb), "")
				stateManifest = state.UpdatePhase(stateManifest, operationLogId, componentName, "success")
				stateUpdater(stateManifest)
			}
		}
	}

	stackReadyConditionFailed := false
	if isDeploy {
		err := waitForReadyConditions(stackManifest.Lifecycle.ReadyConditions, stackParameters, allOutputs)
		if err != nil {
			message := fmt.Sprintf("Stack ready condition failed: %v", err)
			if stateManifest != nil {
				stateManifest = state.UpdateStackStatus(stateManifest, "incomplete", message)
				stateManifest = state.UpdateOperation(stateManifest, operationLogId, request.Verb, "error", nil)
				stateUpdater(stateManifest)
			}
			util.MaybeFatalf("%s", message)
			stackReadyConditionFailed = true
		}
	}

	if stateManifest != nil {
		if !stackReadyConditionFailed {
			status, message := calculateStackStatus(stackManifest, stateManifest)
			stateManifest = state.UpdateStackStatus(stateManifest, status, message)
			stateManifest = state.UpdateOperation(stateManifest, operationLogId, request.Verb, "success", nil)
			stateUpdater(stateManifest)
		}
		stateUpdater("sync")
	}

	var stackOutputs []parameters.ExpandedOutput
	if stateManifest != nil {
		stackOutputs = stateManifest.StackOutputs
	} else {
		stackOutputs = parameters.ExpandRequestedOutputs(stackParameters, allOutputs, stackManifest.Outputs, false)
	}

	if config.Verbose {
		if isDeploy {
			if len(provides) > 0 {
				log.Printf("%s provides:", strings.Title(stackManifest.Kind))
				util.PrintDeps(noEnvironmentProvides(provides))
			}
			printStackOutputs(stackOutputs)
		}
	}

	if request.SaveStackInstanceOutputs && request.StackInstance != "" && len(stackOutputs) > 0 {
		patch := api.StackInstancePatch{
			Outputs:  api.TransformStackOutputsToApi(stackOutputs),
			Provides: noEnvironmentProvides(provides),
		}
		if config.Verbose {
			log.Print("Sending stack outputs to SuperHub")
			if config.Trace {
				printStackInstancePatch(patch)
			}
		}
		_, err := api.PatchStackInstance(request.StackInstance, patch)
		if err != nil {
			util.Warn("Unable to send stack outputs to SuperHub: %v", err)
		}
	}

	if config.Verbose {
		printEndBlurb(request, stackManifest)
	}
}

func optionalComponent(lifecycle *manifest.Lifecycle, componentName string) bool {
	return (len(lifecycle.Mandatory) > 0 && !util.Contains(lifecycle.Mandatory, componentName)) ||
		util.Contains(lifecycle.Optional, componentName)
}

func maybeFatalIfMandatory(lifecycle *manifest.Lifecycle, componentName string, msg string, cleanup func(string, bool)) {
	if optionalComponent(lifecycle, componentName) {
		util.Warn("%s", msg)
		if cleanup != nil {
			cleanup(msg, false)
		}
	} else {
		util.MaybeFatalf2(cleanup, "%s", msg)
	}
}

func addLockedParameter(params parameters.LockedParameters, name, env, value string) {
	if p, exist := params[name]; !exist || p.Value == "" {
		if exist && p.Env != "" {
			env = p.Env
		}
		if config.Verbose {
			log.Printf("Adding implicit parameter %s = `%s` (env: %s)", name, value, env)
		}
		params[name] = parameters.LockedParameter{Name: name, Value: value, Env: env}
	}
}

func addLockedParameter2(params []parameters.LockedParameter, name, env, value string) []parameters.LockedParameter {
	for i := range params {
		p := &params[i]
		if p.Name == name {
			if p.Env == "" {
				p.Env = env
			}
			if p.Value == "" {
				p.Value = value
			}
			return params
		}
	}
	if config.Verbose {
		log.Printf("Adding implicit parameter %s = `%s` (env: %s)", name, value, env)
	}
	return append(params, parameters.LockedParameter{Name: name, Value: value, Env: env})
}

func addHubProvides(params []parameters.LockedParameter, provides map[string][]string) []parameters.LockedParameter {
	return addLockedParameter2(params, "hub.provides", "HUB_PROVIDES", strings.Join(util.SortedKeys2(provides), " "))
}

func maybeTestVerb(verb string, test bool) string {
	if test {
		return verb + "-test"
	}
	return verb
}

func delegate(verb string, component *manifest.ComponentRef, componentManifest *manifest.Manifest,
	componentParameters parameters.LockedParameters,
	dir string, osEnv []string, pipeOutputInRealtime bool) ([]byte, []byte, error) {

	if config.Debug && len(componentParameters) > 0 {
		log.Print("Component parameters:")
		parameters.PrintLockedParameters(componentParameters)
	}

	componentName := manifest.ComponentQualifiedNameFromRef(component)
	errs := processTemplates(component, &componentManifest.Templates, componentParameters, nil, dir)
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("Failed to process templates:\n\t%s", util.Errors("\n\t", errs...))
	}

	processEnv := parametersInEnv(componentName, componentParameters)
	impl, err := findImplementation(dir, verb)
	if err != nil {
		if componentManifest.Lifecycle.Bare == "allow" {
			if config.Verbose {
				log.Printf("Skip `%s`: %v", componentName, err)
			}
			return nil, nil, nil
		}
		return nil, nil, err
	}
	impl.Env = mergeOsEnviron(osEnv, processEnv)
	if config.Debug && len(processEnv) > 0 {
		log.Print("Component environment:")
		printEnvironment(processEnv)
		if config.Trace {
			log.Print("Full process environment:")
			printEnvironment(impl.Env)
		}
	}

	stdout, stderr, err := execImplementation(impl, pipeOutputInRealtime)
	if !pipeOutputInRealtime && (config.Trace || err != nil) {
		log.Print(formatStdoutStderr(stdout, stderr))
	}
	return stdout, stderr, err
}

func parametersInEnv(componentName string, componentParameters parameters.LockedParameters) []string {
	envParameters := make([]string, 0)
	envSetBy := make(map[string]string)
	envValue := make(map[string]string)
	for _, parameter := range componentParameters {
		if parameter.Env == "" {
			continue
		}
		currentValue := strings.TrimSpace(parameter.Value)
		envParameters = append(envParameters, fmt.Sprintf("%s=%s", parameter.Env, currentValue))
		name := parameter.QName()
		setBy, exist := envSetBy[parameter.Env]
		if exist {
			prevValue := envValue[parameter.Env]
			if prevValue != currentValue { /*||
				(!strings.HasPrefix(setBy, name+"|") && !strings.HasPrefix(name, setBy+"|")) {*/
				util.Warn("Env var `%s=%s` set by `%s` overriden by `%s` to `%s`",
					parameter.Env, prevValue, setBy, name, currentValue)
			}
		}
		envSetBy[parameter.Env] = name
		envValue[parameter.Env] = currentValue
	}

	envComponentName := "COMPONENT_NAME"
	if setBy, exist := envSetBy[envComponentName]; !exist {
		envParameters = append(envParameters, fmt.Sprintf("%s=%s", envComponentName, componentName))
	} else if config.Debug && envValue[envComponentName] != componentName {
		log.Printf("Component `%s` env var `%s=%s` set by `%s`",
			componentName, envComponentName, envValue[envComponentName], setBy)
	}
	// for `hub render`
	envParameters = append(envParameters, fmt.Sprintf("%s=%s", HubEnvVarNameComponentName, componentName))

	return mergeOsEnviron(envParameters) // sort
}
