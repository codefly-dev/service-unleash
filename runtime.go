package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	runnersbase "github.com/codefly-dev/core/runners/base"
	dockerrun "github.com/codefly-dev/core/runners/dockerrun"
)

const (
	postgresPort = 5432
	adminPort    = 4242
	edgePort     = 3063
)

type localComponent struct {
	name          string
	image         *resources.DockerImage
	hostPort      uint16
	containerPort uint16
	command       []string
	environment   []*resources.EnvironmentVariable
	cacheKey      string
	cacheTarget   string
}

func localComposition(postgresHostPort, adminHostPort, edgeHostPort uint16, postgresPassword, adminToken, clientToken string) []localComponent {
	return []localComponent{
		{
			name: "postgres", image: postgresImage, hostPort: postgresHostPort, containerPort: postgresPort,
			environment: []*resources.EnvironmentVariable{
				resources.Env("POSTGRES_DB", "unleash"),
				resources.Env("POSTGRES_USER", "unleash"),
				resources.Env("POSTGRES_PASSWORD", postgresPassword),
			},
			cacheKey: "postgres-data", cacheTarget: "/var/lib/postgresql/data",
		},
		{
			name: "server", image: serverImage, hostPort: adminHostPort, containerPort: adminPort,
			environment: []*resources.EnvironmentVariable{
				resources.Env("DATABASE_HOST", "host.docker.internal"),
				resources.Env("DATABASE_PORT", postgresHostPort),
				resources.Env("DATABASE_NAME", "unleash"),
				resources.Env("DATABASE_USERNAME", "unleash"),
				resources.Env("DATABASE_PASSWORD", postgresPassword),
				resources.Env("DATABASE_SSL", false),
				resources.Env("LOG_LEVEL", "warn"),
				resources.Env("INIT_ADMIN_API_TOKENS", adminToken),
				resources.Env("INIT_BACKEND_API_TOKENS", clientToken),
			},
		},
		{
			name: "edge", image: edgeImage, hostPort: edgeHostPort, containerPort: edgePort,
			command: []string{"edge"},
			environment: []*resources.EnvironmentVariable{
				resources.Env("UPSTREAM_URL", fmt.Sprintf("http://host.docker.internal:%d", adminHostPort)),
				resources.Env("TOKENS", clientToken),
				resources.Env("FEATURES_REFRESH_INTERVAL_SECONDS", 1),
			},
		},
	}
}

type Runtime struct {
	services.RuntimeServer
	*Service

	postgresRunner *dockerrun.DockerEnvironment
	serverRunner   *dockerrun.DockerEnvironment
	edgeRunner     *dockerrun.DockerEnvironment

	postgresHostPort uint16
	adminHostPort    uint16
	edgeHostPort     uint16
}

func NewRuntime() *Runtime {
	return &Runtime{Service: NewService()}
}

func (s *Runtime) Load(ctx context.Context, req *runtimev0.LoadRequest) (*runtimev0.LoadResponse, error) {
	defer s.Wool.Catch()

	return s.Runtime.LoadService(ctx, req, services.RuntimeLoad{
		Settings:     s.Settings,
		Requirements: requirements,
		ResolveEndpoints: func(ctx context.Context, endpoints []*basev0.Endpoint) error {
			return s.Service.resolveEndpoints(ctx, endpoints)
		},
	})
}

func (s *Runtime) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	s.Runtime.LogInitRequest(req)
	s.Runtime.WithContext(req.GetRuntimeContext())
	s.NetworkMappings = req.GetProposedNetworkMappings()

	if err := s.loadSecrets(ctx, req.GetConfiguration()); err != nil {
		return s.Runtime.InitError(err)
	}
	adminInstance, err := resources.FindNetworkInstanceInNetworkMappings(ctx, s.NetworkMappings, s.adminEndpoint, resources.NewNativeNetworkAccess())
	if err != nil {
		return s.Runtime.InitError(err)
	}
	edgeInstance, err := resources.FindNetworkInstanceInNetworkMappings(ctx, s.NetworkMappings, s.edgeEndpoint, resources.NewNativeNetworkAccess())
	if err != nil {
		return s.Runtime.InitError(err)
	}
	freePort, err := runnersbase.FindFreePort()
	if err != nil {
		return s.Runtime.InitError(err)
	}
	s.postgresHostPort = uint16(freePort)
	s.adminHostPort = uint16(adminInstance.GetPort())
	s.edgeHostPort = uint16(edgeInstance.GetPort())

	components := localComposition(s.postgresHostPort, s.adminHostPort, s.edgeHostPort, s.postgresPassword, s.adminToken, s.clientToken)
	for _, component := range components {
		runner, createErr := dockerrun.NewDockerHeadlessEnvironment(ctx, component.image, s.UniqueWithWorkspace()+"-"+component.name)
		if createErr != nil {
			s.shutdownStarted(ctx)
			return s.Runtime.InitError(createErr)
		}
		runner.WithOutput(s.Wool)
		runner.WithPortMapping(ctx, component.hostPort, component.containerPort)
		runner.WithEnvironmentVariables(ctx, component.environment...)
		if len(component.command) > 0 {
			runner.WithCommand(component.command...)
		}
		if component.cacheKey != "" {
			if _, mountErr := runner.WithPersistentCacheMount(ctx, component.cacheKey, component.cacheTarget); mountErr != nil {
				s.shutdownStarted(ctx)
				return s.Runtime.InitError(mountErr)
			}
		}
		s.setRunner(component.name, runner)
		if initErr := runner.Init(ctx); initErr != nil {
			s.shutdownStarted(ctx)
			return s.Runtime.InitError(initErr)
		}
		switch component.name {
		case "postgres":
			if readyErr := waitForTCP(ctx, fmt.Sprintf("127.0.0.1:%d", s.postgresHostPort), 90*time.Second); readyErr != nil {
				s.shutdownStarted(ctx)
				return s.Runtime.InitError(readyErr)
			}
		case "server":
			if readyErr := waitForHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/health", s.adminHostPort), 120*time.Second); readyErr != nil {
				s.shutdownStarted(ctx)
				return s.Runtime.InitError(readyErr)
			}
		}
	}

	s.addRuntimeConfigurations()
	return s.Runtime.InitResponse()
}

func (s *Runtime) setRunner(name string, runner *dockerrun.DockerEnvironment) {
	switch name {
	case "postgres":
		s.postgresRunner = runner
	case "server":
		s.serverRunner = runner
	case "edge":
		s.edgeRunner = runner
	}
}

func (s *Runtime) addRuntimeConfigurations() {
	byContext := make(map[string][]*basev0.ConfigurationInformation)
	for _, endpoint := range []*basev0.Endpoint{s.adminEndpoint, s.edgeEndpoint} {
		mapping, err := resources.FindNetworkMapping(context.Background(), s.NetworkMappings, endpoint)
		if err != nil {
			continue
		}
		for _, instance := range mapping.GetInstances() {
			kind := resources.RuntimeContextFromInstance(instance).GetKind()
			if kind != resources.RuntimeContextNative && kind != resources.RuntimeContextContainer {
				continue
			}
			connection := instance.GetAddress()
			token := s.clientToken
			if endpoint.GetName() == "admin" {
				connection = fmt.Sprintf("http://%s:%d", instance.GetHostname(), instance.GetPort())
				token = s.adminToken
			}
			byContext[kind] = append(byContext[kind], &basev0.ConfigurationInformation{
				Name: endpoint.GetName(),
				ConfigurationValues: []*basev0.ConfigurationValue{
					{Key: "connection", Value: connection},
					{Key: "token", Value: token, Secret: true},
				},
			})
		}
	}
	for _, runtimeContext := range []*basev0.RuntimeContext{resources.NewRuntimeContextNative(), resources.NewRuntimeContextContainer()} {
		if infos := byContext[runtimeContext.GetKind()]; len(infos) > 0 {
			s.Runtime.RuntimeConfigurations = append(s.Runtime.RuntimeConfigurations, &basev0.Configuration{
				Origin: s.Unique(), RuntimeContext: runtimeContext, Infos: infos,
			})
		}
	}
}

func waitForTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s was not ready within %s", address, timeout)
}

func waitForHTTP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s was not ready within %s", address, timeout)
}

func (s *Runtime) Start(ctx context.Context, _ *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	if err := waitForHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/internal-backstage/ready", s.edgeHostPort), 60*time.Second); err != nil {
		return s.Runtime.StartError(err)
	}
	return s.Runtime.StartResponse()
}

func (s *Runtime) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return s.Runtime.InformationResponse(ctx, req)
}

func (s *Runtime) Stop(context.Context, *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	return s.Runtime.StopResponse()
}

func (s *Runtime) Destroy(ctx context.Context, _ *runtimev0.DestroyRequest) (*runtimev0.DestroyResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	if err := s.shutdownStarted(ctx); err != nil {
		return s.Runtime.DestroyError(err)
	}
	return s.Runtime.DestroyResponse()
}

func (s *Runtime) shutdownStarted(ctx context.Context) error {
	var first error
	for _, runner := range []*dockerrun.DockerEnvironment{s.edgeRunner, s.serverRunner, s.postgresRunner} {
		if runner == nil {
			continue
		}
		if err := runner.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Runtime) Test(context.Context, *runtimev0.TestRequest) (*runtimev0.TestResponse, error) {
	return s.Runtime.TestResponse()
}
