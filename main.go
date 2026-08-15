package main

import (
	"context"
	"embed"
	"fmt"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/builders"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/codefly-dev/core/resources"
	runnersbase "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	configurationName   = "unleash"
	postgresPasswordKey = "POSTGRES_PASSWORD"
	adminTokenKey       = "UNLEASH_ADMIN_TOKEN"
	clientTokenKey      = "UNLEASH_CLIENT_TOKEN"
)

var agent = shared.Must(resources.LoadFromFs[resources.Agent](shared.Embed(infoFS)))

var requirements = builders.NewDependencies(agent.Name,
	builders.NewDependency("service.codefly.yaml"),
)

type Settings struct{}

var serverImage = &resources.DockerImage{
	Name:   "unleashorg/unleash-server",
	Tag:    "8.1.0",
	Digest: "sha256:16f3ffb914880e7d0f23629a0c1b77aebea3aa619b0305f76eb50b3fb75998a9",
}

var edgeImage = &resources.DockerImage{
	Name:   "unleashorg/unleash-edge",
	Tag:    "v20.4.1",
	Digest: "sha256:16fb3d481eb2fc8b5981ed1c3d32b684c47ad9d6e41e375dcaaaee27991e6368",
}

var postgresImage = &resources.DockerImage{
	Name:   "postgres",
	Tag:    "17.8-alpine",
	Digest: "sha256:3430fe182f5065a6ea505c3d432d2c7fff18fbab954df8f277c1dbf4c70124af",
}

type Service struct {
	*services.Base
	*Settings

	adminEndpoint *basev0.Endpoint
	edgeEndpoint  *basev0.Endpoint

	postgresPassword string
	adminToken       string
	clientToken      string
}

func NewService() *Service {
	return &Service{
		Base:     services.NewServiceBase(context.Background(), agent.Of(resources.ServiceAgent)),
		Settings: &Settings{},
	}
}

func (s *Service) GetAgentInformation(ctx context.Context, _ *agentv0.AgentInformationRequest) (*agentv0.AgentInformation, error) {
	defer s.Wool.Catch()

	readme, err := templates.ApplyTemplateFrom(ctx, shared.Embed(readmeFS), "templates/agent/README.md", s.Information)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return services.Advertisement{
		Backends: runnersbase.BackendSupport{Docker: true},
		Config: []*agentv0.ConfigurationValueDetail{
			{
				Name: "admin", Description: "Module-visible Unleash administration endpoint",
				Fields: []*agentv0.ConfigurationValueInformation{
					{Name: "connection", Description: "Unleash admin and API URL"},
					{Name: "token", Description: "Unleash admin API token"},
				},
			},
			{
				Name: "edge", Description: "Public Unleash Edge endpoint",
				Fields: []*agentv0.ConfigurationValueInformation{
					{Name: "connection", Description: "Unleash Edge URL"},
					{Name: "token", Description: "Unleash client API token"},
				},
			},
		},
		ReadMe: readme,
	}.Build(), nil
}

func (s *Service) loadSecrets(ctx context.Context, configuration *basev0.Configuration) error {
	values := []struct {
		key         string
		destination *string
	}{
		{postgresPasswordKey, &s.postgresPassword},
		{adminTokenKey, &s.adminToken},
		{clientTokenKey, &s.clientToken},
	}
	for _, value := range values {
		resolved, err := resources.GetConfigurationValue(ctx, configuration, configurationName, value.key)
		if err != nil {
			return err
		}
		if resolved == "" {
			return fmt.Errorf("missing Codefly secret configuration %s", value.key)
		}
		*value.destination = resolved
	}
	return nil
}

func main() {
	agents.Serve(agents.PluginRegistration{
		Agent:   NewService(),
		Runtime: NewRuntime(),
		Builder: NewBuilder(),
	})
}

//go:embed agent.codefly.yaml
var infoFS embed.FS

//go:embed templates/agent
var readmeFS embed.FS
