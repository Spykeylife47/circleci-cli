package api

import (
	"context"
	"io/ioutil"
	"strings"

	"fmt"

	"github.com/CircleCI-Public/circleci-cli/client"
	"github.com/CircleCI-Public/circleci-cli/logger"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// GQLResponseErrors is a slice of errors returned by the GraphQL server. Each
// error message is a key-value pair with the structure "Message: string"
type GQLResponseErrors struct {
	Errors []struct {
		Message string
	}
}

// ConfigResponse is a structure that matches the result of the GQL
// query, so that we can use mapstructure to convert from
// nested maps to a strongly typed struct.
type ConfigResponse struct {
	Valid      bool
	SourceYaml string
	OutputYaml string

	GQLResponseErrors
}

// The PublishOrbResponse type matches the data shape of the GQL response for
// publishing an orb.
type PublishOrbResponse struct {
	Orb struct {
		CreatedAt string
		Version   string
	}

	GQLResponseErrors
}

// CreateNamespaceResponse type matches the data shape of the GQL response for
// creating a namespace
type CreateNamespaceResponse struct {
	Namespace struct {
		CreatedAt string
		ID        string
	}

	GQLResponseErrors
}

// CreateOrbResponse type matches the data shape of the GQL response for
// creating an orb
type CreateOrbResponse struct {
	Orb struct {
		CreatedAt string
		ID        string
	}

	GQLResponseErrors
}

// ToError returns all GraphQL errors for a single response concatenated, or
// nil.
func (response GQLResponseErrors) ToError() error {
	messages := []string{}

	for i := range response.Errors {
		messages = append(messages, response.Errors[i].Message)
	}

	return errors.New(strings.Join(messages, ": "))
}

func loadYaml(path string) (string, error) {

	config, err := ioutil.ReadFile(path)

	if err != nil {
		return "", errors.Wrapf(err, "Could not load config file at %s", path)
	}

	return string(config), nil
}

func buildAndOrbQuery(ctx context.Context, logger *logger.Logger, configPath string, response interface{}, query string) error {
	config, err := loadYaml(configPath)
	if err != nil {
		return err
	}

	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("config", config)
	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err = graphQLclient.Run(ctx, request, response)

	if err != nil {
		return errors.Wrap(err, "Unable to validate config")
	}

	return nil
}

// ConfigQuery calls the GQL API to validate and expand config
func ConfigQuery(ctx context.Context, logger *logger.Logger, configPath string) (*ConfigResponse, error) {
	var response struct {
		BuildConfig struct {
			ConfigResponse
		}
	}
	return &response.BuildConfig.ConfigResponse, buildAndOrbQuery(ctx, logger, configPath, &response, `
		query ValidateConfig ($config: String!) {
			buildConfig(configYaml: $config) {
				valid,
				errors { message },
				sourceYaml,
				outputYaml
			}
		}`)
}

// OrbQuery validated and expands an orb.
func OrbQuery(ctx context.Context, logger *logger.Logger, configPath string) (*ConfigResponse, error) {
	var response struct {
		OrbConfig struct {
			ConfigResponse
		}
	}

	return &response.OrbConfig.ConfigResponse, buildAndOrbQuery(ctx, logger, configPath, &response, `
		query ValidateOrb ($config: String!) {
			orbConfig(orbYaml: $config) {
				valid,
				errors { message },
				sourceYaml,
				outputYaml
			}
		}`)
}

// OrbPublish publishes a new version of an orb
func OrbPublish(ctx context.Context, logger *logger.Logger,
	configPath string, orbVersion string, orbID string) (*PublishOrbResponse, error) {
	var response struct {
		PublishOrb struct {
			PublishOrbResponse
		}
	}

	config, err := loadYaml(configPath)
	if err != nil {
		return nil, err
	}

	query := `
		mutation($config: String!, $orbId: UUID!, $version: String!) {
			publishOrb(
				orbId: $orbId,
				orbYaml: $config,
				version: $version
			) {
				orb {
					version
					createdAt
				}
				errors { message }
			}
		}
	`

	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("config", config)
	request.Var("orbId", orbID)
	request.Var("version", orbVersion)

	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err = graphQLclient.Run(ctx, request, &response)

	if err != nil {
		err = errors.Wrap(err, "Unable to publish orb")
	}
	return &response.PublishOrb.PublishOrbResponse, err
}

func createNamespaceWithOwnerID(ctx context.Context, logger *logger.Logger, name string, ownerID string) (*CreateNamespaceResponse, error) {
	var response struct {
		CreateNamespace struct {
			CreateNamespaceResponse
		}
	}

	query := `
			mutation($name: String!, $organizationId: UUID!) {
				createNamespace(
					name: $name,
					organizationId: $organizationId
				) {
					namespace {
						createdAt
						id
					}
					errors {
						message
						type
					}
				}
			}`

	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("name", name)
	request.Var("organizationId", ownerID)

	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err := graphQLclient.Run(ctx, request, &response)

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("Unable to create namespace %s for ownerId %s", name, ownerID))
	}

	return &response.CreateNamespace.CreateNamespaceResponse, err
}

func getOrganization(ctx context.Context, logger *logger.Logger, organizationName string, organizationVcs string) (string, error) {
	var response struct {
		Organization struct {
			ID string
		}
	}

	query := `
			query($organizationName: String!, $organizationVcs: VCSType!) {
				organization(
					name: $organizationName
					vcsType: $organizationVcs
				) {
					id
				}
			}`

	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("organizationName", organizationName)
	request.Var("organizationVcs", organizationVcs)

	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err := graphQLclient.Run(ctx, request, &response)

	if err != nil || response.Organization.ID == "" {
		err = errors.New(fmt.Sprintf("Unable to find organization %s of vcs-type %s", organizationName, organizationVcs))
	}

	return response.Organization.ID, err
}

// CreateNamespace creates (reserves) a namespace for an organization
func CreateNamespace(ctx context.Context, logger *logger.Logger, name string, organizationName string, organizationVcs string) (*CreateNamespaceResponse, error) {
	organizationID, err := getOrganization(ctx, logger, organizationName, organizationVcs)
	if err != nil {
		return nil, err
	}

	namespace, err := createNamespaceWithOwnerID(ctx, logger, name, organizationID)

	if err != nil {
		return nil, err
	}

	return namespace, err
}


func getNamespace(ctx context.Context, logger *logger.Logger, name string) (string, error){
	var response struct {
		Namespace struct {
			ID string
		}
	}

	query := `
				query($name: String!) {
					registryNamespace(
						name: $name
					){
						id
					}
			 }`
	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("name", name)

	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err := graphQLclient.Run(ctx, request, & response)
fmt.Println("response is: ", response)

	if err != nil || response.Namespace.ID == "" {
		err = errors.New(fmt.Sprintf("Unable to find namespace %s", name))
	}

	return response.Namespace.ID, err
}

func createOrbWithNsID(ctx context.Context, logger *logger.Logger, name string, namespaceID string) (*CreateOrbResponse, error) {
	var response struct {
		CreateOrb struct {
			CreateOrbResponse
		}
	}

	query := `mutation($name: String!, $registryNamespaceId: UUID!){
				createOrb(
					name: $name,
					registryNamespaceId: $registryNamespaceId
				){
					createdAt
					id
				}
					errors {
						message
						type
					}
				}`

	request := client.NewAuthorizedRequest(viper.GetString("token"), query)
	request.Var("name", name)
	request.Var("registryNamespaceId", namespaceID)

	graphQLclient := client.NewClient(viper.GetString("endpoint"), logger)

	err := graphQLclient.Run(ctx, request, &response)

	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("Unable to create orb %s for namespaceID %s", name, namespaceID))
	}

	return &response.CreateOrb.CreateOrbResponse, err
}


// CreateOrb creates (reserves) an orb within a namespace
func CreateOrb(ctx context.Context, logger *logger.Logger, name string, namespace string) (*CreateOrbResponse, error) {
	fmt.Println("---- here we go")
	namespaceID, err := getNamespace(ctx, logger, namespace)

	if err != nil {
		return nil, err
	}

	orb, err := createOrbWithNsID(ctx, logger, name, namespaceID)
	fmt.Println("---- in theory, orb is:", orb)

	if err != nil {
		return nil, err
	}

	return orb, err
}