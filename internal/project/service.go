package project

import (
	"bytes"
	"debugged-dev/kip/v1/internal/generator"
	"debugged-dev/kip/v1/internal/version"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"robpike.io/filter"
)

type ServiceProject struct {
	path    string
	config  *viper.Viper
	project *MonoProject
}

func CreateServiceProject(path string, name string, generatorName string, args []string) error {
	p := ServiceProject{path: filepath.Join(path, name)}
	return p.New(name, generatorName, args)
}

func (s ServiceProject) Name() string {
	return filepath.Base(s.path)
}

func (s ServiceProject) Template() string {
	return s.config.GetString("template")
}

func (s ServiceProject) Environment() string {
	return s.config.GetString("environment")
}

func (s ServiceProject) Repository() string {

	if s.project != nil && !s.config.IsSet("repository") {
		return s.project.Repository()
	}

	return s.config.GetString("repository")
}

func (s ServiceProject) Version() string {
	return s.config.GetString("version")
}

func (s ServiceProject) BuildPath() string {
	buildPath := s.Paths().BuildPathTemplate

	if s.project != nil && !s.config.IsSet("buildPath") {
		buildPath = s.project.Paths().BuildPathTemplate
		buildPath = strings.ReplaceAll(buildPath, "<projectDir>", s.project.Paths().Root)
	}

	buildPath = strings.ReplaceAll(buildPath, "<projectDir>", s.Paths().Root)
	buildPath = strings.ReplaceAll(buildPath, "<serviceDir>", s.Paths().Root)

	return buildPath
}

func (s ServiceProject) Paths() paths {
	buildPathTemplate := "<projectDir>"

	if s.config != nil && s.config.IsSet("buildPath") && len(s.config.GetString("buildPath")) > 0 {
		buildPathTemplate = s.config.GetString("buildPath")
	}

	return paths{
		Root:              s.path,
		Deployments:       filepath.Join(s.path, "deployments"),
		Environments:      filepath.Join(s.path, "environments"),
		Scripts:           filepath.Join(s.path, "scripts"),
		BuildPathTemplate: buildPathTemplate,
	}
}

func (s ServiceProject) New(name string, generatorName string, args []string) error {

	paths := s.Paths()

	if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
		return fmt.Errorf("folder %s already exist", name)
	}

	err := generator.Generate(generatorName, filepath.Join(paths.Root, ".."), name, args)

	if err != nil {
		return err
	}

	os.MkdirAll(paths.Services, os.ModePerm)
	os.MkdirAll(paths.Environments, os.ModePerm)
	os.MkdirAll(paths.Deployments, os.ModePerm)

	config := viper.New()
	config.AddConfigPath(paths.Root)
	config.SetConfigName("kip_config")
	config.SetConfigType("yaml")

	config.Set("template", "service")
	config.Set("version", version.Get().Version)
	config.Set("environment", "dev")

	config.SafeWriteConfig()

	return nil
}

func (s ServiceProject) Services() []ServiceProject {
	return []ServiceProject{s}
}

func (p ServiceProject) GetService(name string) (*ServiceProject, error) {
	return nil, errors.New("not implemented")
}

func (s ServiceProject) Charts() []Chart {
	return getCharts(s.Paths().Deployments, s.Name(), s)
}

func (s ServiceProject) AddChart(chartName string, args []string) (string, error) {
	return createChart(chartName, s.Paths().Deployments, args)
}

func (s ServiceProject) GetScript(name string) (*Script, error) {
	for _, script := range s.GetScripts("", "") {
		if script.Name == name {
			return &script, nil
		}
	}

	return nil, fmt.Errorf("script \"%s\" not found", name)
}

func (s ServiceProject) GetScripts(binding string, environment string) []Script {
	var scripts []Script
	err := s.config.UnmarshalKey("scripts", &scripts)

	if err != nil {
		log.Fatalf("unable to decode into struct, %v", err)
	}

	scripts = filter.Apply(scripts, func(script Script) Script {
		script.Path = filepath.Join(s.Paths().Scripts, script.Name)
		return script
	}).([]Script)

	if binding != "" {
		scripts = filter.Choose(scripts, func(s Script) bool {
			for _, value := range s.Bindings {
				if value == binding {
					return true
				}
			}
			return false
		}).([]Script)
	}

	if environment != "" {
		scripts = filter.Choose(scripts, func(s Script) bool {

			if len(s.Environments) == 0 {
				return true
			}

			for _, value := range s.Environments {
				if value == environment {
					return true
				}
			}
			return false
		}).([]Script)
	}

	return scripts
}

func (s ServiceProject) AddScript(scriptName string, command string, bindings []string) error {
	config := scriptConfig{Name: scriptName, Command: command, Bindings: bindings}

	scriptConfigs := []scriptConfig{}

	for _, script := range s.GetScripts("", "") {
		scriptConfigs = append(scriptConfigs, scriptConfig{Name: script.Name, Command: script.Command, Bindings: script.Bindings})
	}

	scriptConfigs = append(scriptConfigs, config)

	s.config.Set("scripts", scriptConfigs)

	err := s.config.WriteConfig()

	return err
}

func (s ServiceProject) HasDockerfile() bool {
	_, err := os.Stat(filepath.Join(s.path, "Dockerfile"))
	return !os.IsNotExist(err)
}

func (s ServiceProject) Build(repository string, key string, args []string) error {
	dockerfilePath := filepath.Join(s.Paths().Root, "Dockerfile")

	servicePath, err := filepath.Rel(s.BuildPath(), dockerfilePath)

	if repository == "" {
		repository = s.Repository()
	}

	if err != nil {
		fmt.Println(err)
		return err
	}

	tempId := "temp-" + key

	cmdArgs := []string{"build", s.BuildPath(), "-f", servicePath, "-t", repository + s.Name() + ":" + tempId}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Dir = s.BuildPath()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()

	if err != nil {
		fmt.Println(err)
		return err
	}

	tempID, err := s.GetImageID(tempId, repository)

	if err != nil {
		fmt.Println(err)
		return err
	}

	err = s.TagImage(tempId, tempID, repository)

	if err != nil {
		fmt.Println(err)
		return err
	}

	err = s.TagImage(tempId, key, repository)

	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}

func (s ServiceProject) Push(repository string, key string, args []string) error {

	if repository == "" {
		repository = s.Repository()
	}

	imageID, err := s.GetImageID(key, repository)

	if err != nil {
		fmt.Println(err)
		return err
	}

	cmdArgs := []string{"push", repository + s.Name() + ":" + imageID}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Dir = s.Paths().Root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()

	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}

func (s ServiceProject) GetImageID(tag string, repository string) (string, error) {
	cmd := exec.Command("docker", "inspect", "--format", "{{.Id}}", repository+s.Name()+":"+tag)
	cmd.Dir = s.Paths().Root
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()

	if err != nil {
		return "", err
	}

	imageID := strings.TrimSpace(out.String())

	return imageID[7:19], nil
}

func (s ServiceProject) TagImage(currentTag string, newTag string, repository string) error {

	cmd := exec.Command("docker", "tag", repository+s.Name()+":"+currentTag, repository+s.Name()+":"+newTag)
	cmd.Dir = s.Paths().Root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()

	if err != nil {
		return err
	}

	fmt.Printf("Successfully tagged %s:%s\n", s.Name(), newTag)

	return nil
}

func getServices(path string, project *MonoProject) []ServiceProject {
	services := []ServiceProject{}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Fatal(err)
	}

	for _, f := range files {
		if f.IsDir() {
			serviceName := f.Name()
			servicePath := filepath.Join(path, serviceName)

			serviceConfig := viper.New()
			serviceConfig.AddConfigPath(servicePath)
			serviceConfig.SetConfigName("kip_config")
			serviceConfig.SetConfigType("yaml")

			serviceConfig.AutomaticEnv()

			err := serviceConfig.ReadInConfig()

			if err != nil {
				log.Fatal(err)
			}

			var s ServiceProject
			s = ServiceProject{path: servicePath, project: project, config: serviceConfig}
			services = append(services, s)
		}
	}

	return services
}
