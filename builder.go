package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

type Builder struct {
	*services.DefaultBuilder
	*Service
}

type deploymentTemplateParameters struct {
	ServerImage               string
	EdgeImage                 string
	PostgresImage             string
	PostgresPasswordReference *builderv0.KubernetesSecretKeyReference
	AdminTokenReference       *builderv0.KubernetesSecretKeyReference
	ClientTokenReference      *builderv0.KubernetesSecretKeyReference
}

func defaultDeploymentTemplateParameters() *deploymentTemplateParameters {
	return &deploymentTemplateParameters{
		ServerImage:   serverImage.FullName(),
		EdgeImage:     edgeImage.FullName(),
		PostgresImage: postgresImage.FullName(),
	}
}

func NewBuilder() *Builder {
	service := NewService()
	return &Builder{
		DefaultBuilder: services.NewDefaultBuilder(service.Builder),
		Service:        service,
	}
}

func (s *Builder) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	defer s.Wool.Catch()

	return s.Builder.LoadService(ctx, req, services.BuilderLoad{
		Settings:         s.Settings,
		Requirements:     requirements,
		FactoryTemplates: factoryFS,
		ResolveEndpoints: s.resolveEndpoints,
	})
}

func (s *Service) resolveEndpoints(ctx context.Context, endpoints []*basev0.Endpoint) error {
	admin, err := resources.FindTCPEndpointWithName(ctx, "admin", endpoints)
	if err != nil {
		return err
	}
	edge, err := findEndpointByName(endpoints, "edge")
	if err != nil {
		return err
	}
	if edge.GetApi() != standards.HTTP {
		return fmt.Errorf("endpoint %q must use the HTTP API", edge.GetName())
	}
	s.adminEndpoint = admin
	s.edgeEndpoint = edge
	return nil
}

func findEndpointByName(endpoints []*basev0.Endpoint, name string) (*basev0.Endpoint, error) {
	for _, endpoint := range endpoints {
		if endpoint.GetName() == name {
			return endpoint, nil
		}
	}
	return nil, fmt.Errorf("endpoint %q not found", name)
}

func (s *Builder) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	s.Builder.LogInitRequest(req)
	return s.Builder.InitResponse()
}

func (s *Builder) Audit(ctx context.Context, req *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return s.Builder.AuditContainer(ctx, req, serverImage.FullName())
}

func (s *Builder) SBOM(ctx context.Context, _ *builderv0.SBOMRequest) (*builderv0.SBOMResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	return s.Builder.SBOMContainer(ctx, serverImage.FullName())
}

func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)
	s.Base.SetDockerImage(serverImage)

	parameters := defaultDeploymentTemplateParameters()
	var configuration *basev0.Configuration
	response, err := s.Builder.DeployKustomize(ctx, req, services.KustomizeDeployment{
		EnvironmentVariables: s.EnvironmentVariables,
		Templates:            deploymentFS,
		Parameters:           parameters,
		Prepare: func(_ context.Context, deployment *services.KustomizeDeploymentContext) error {
			if prepareErr := s.resolveSecretReferences(deployment.Kubernetes.GetSecretReferences(), parameters); prepareErr != nil {
				return prepareErr
			}
			resolvedConfiguration, prepareErr := s.deploymentConfiguration(req.GetNetworkMappings())
			configuration = resolvedConfiguration
			return prepareErr
		},
	})
	if err != nil || response.GetState().GetState() != builderv0.DeploymentStatus_SUCCESS {
		return response, err
	}
	response.Configuration = configuration
	return response, nil
}

func (s *Builder) resolveSecretReferences(
	references map[string]*builderv0.KubernetesSecretKeyReference,
	parameters *deploymentTemplateParameters,
) error {
	required := []struct {
		key         string
		destination **builderv0.KubernetesSecretKeyReference
	}{
		{postgresPasswordKey, &parameters.PostgresPasswordReference},
		{adminTokenKey, &parameters.AdminTokenReference},
		{clientTokenKey, &parameters.ClientTokenReference},
	}
	for _, secret := range required {
		configurationKey := resources.ServiceSecretConfigurationKeyFromUnique(s.Unique(), configurationName, secret.key)
		reference := references[configurationKey]
		if reference == nil || reference.GetName() == "" || reference.GetKey() == "" {
			return fmt.Errorf("unleash requires a typed Kubernetes Secret reference for %s", configurationKey)
		}
		if reference.GetOptional() {
			return fmt.Errorf("unleash Secret reference %s must not be optional", configurationKey)
		}
		*secret.destination = reference
	}
	return nil
}

func (s *Builder) deploymentConfiguration(mappings []*basev0.NetworkMapping) (*basev0.Configuration, error) {
	configuration := &basev0.Configuration{
		Origin:         s.Unique(),
		RuntimeContext: resources.NewRuntimeContextContainer(),
	}
	for _, endpoint := range []*basev0.Endpoint{s.adminEndpoint, s.edgeEndpoint} {
		instance, err := resources.FindNetworkInstanceInNetworkMappings(context.Background(), mappings, endpoint, resources.NewContainerNetworkAccess())
		if err != nil {
			return nil, err
		}
		connection := instance.GetAddress()
		if endpoint.GetName() == "admin" {
			connection = fmt.Sprintf("http://%s:%d", instance.GetHostname(), instance.GetPort())
		}
		configuration.Infos = append(configuration.Infos, &basev0.ConfigurationInformation{
			Name: endpoint.GetName(),
			ConfigurationValues: []*basev0.ConfigurationValue{
				{Key: "connection", Value: connection},
				{Key: "token", Secret: true},
			},
		})
	}
	return configuration, nil
}

func (s *Builder) Create(ctx context.Context, _ *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	defer s.Wool.Catch()
	ctx = s.Wool.Inject(ctx)

	if err := s.Templates(ctx, struct{}{}, services.WithFactory(factoryFS)); err != nil {
		return s.Builder.CreateError(err)
	}
	if err := s.CreateEndpoints(ctx); err != nil {
		return s.Builder.CreateError(err)
	}
	return s.Builder.CreateResponse(ctx, s.Settings)
}

func (s *Builder) CreateEndpoints(ctx context.Context) error {
	tcpAPI, err := resources.LoadTCPAPI(ctx)
	if err != nil {
		return err
	}
	admin := s.Base.BaseEndpoint("admin")
	admin.Visibility = resources.VisibilityModule
	s.adminEndpoint, err = resources.NewAPI(ctx, admin, resources.ToTCPAPI(tcpAPI))
	if err != nil {
		return err
	}

	httpAPI, err := resources.LoadHTTPAPI(ctx)
	if err != nil {
		return err
	}
	edge := s.Base.BaseEndpoint("edge")
	edge.Visibility = resources.VisibilityPublic
	s.edgeEndpoint, err = resources.NewAPI(ctx, edge, resources.ToHTTPAPI(httpAPI))
	if err != nil {
		return err
	}

	s.Endpoints = []*basev0.Endpoint{s.adminEndpoint, s.edgeEndpoint}
	return nil
}

//go:embed templates/factory
var factoryFS embed.FS

//go:embed templates/deployment
var deploymentFS embed.FS
