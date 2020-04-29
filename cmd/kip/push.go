/*
Copyright © 2020 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"debugged-dev/kip/v1/internal/project"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"robpike.io/filter"
)

type pushOptions struct {
	all         bool
	services    []string
	environment string
	repository  string
	key         string
}

func newPushCmd(out io.Writer) *cobra.Command {
	o := &pushOptions{}

	cmd := &cobra.Command{
		Use:   "push",
		Short: "push images",
		Long: `A longer description that spans multiple lines and likely contains examples
	and usage of using your command. For example:
	
	Cobra is a CLI library for Go that empowers applications.
	This application is a tool to generate the needed files
	to quickly create a Cobra application.`,
		Run: func(cmd *cobra.Command, args []string) {
			if !hasKipConfig {
				log.Fatalln("run this command inside a kip project")
			}

			extraArgs := cmd.Flags().Args()

			services := kipProject.Services()
			servicesToPush := []project.ServiceProject{}

			if !o.all && len(o.services) == 0 {
				o.all = true
			}

			if o.environment == "" {
				o.environment = kipProject.Environment()
			}

			if o.repository == "" {
				o.repository = kipProject.Repository()
			}

			if o.all && len(o.services) > 0 {
				fmt.Fprintf(out, "WARN: --all is ignored when --service is used\n")
				o.all = false
			}

			if o.all {
				servicesToPush = append(servicesToPush, services...)
			} else if len(o.services) > 0 {

				for _, serviceName := range o.services {
					var foundService project.Project = nil
					for _, service := range services {
						if service.Name() == serviceName {
							foundService = service
							break
						}
					}

					if foundService != nil {
						servicesToPush = append(servicesToPush, foundService.(project.ServiceProject))
					} else {
						fmt.Fprintf(out, "service \"%s\" does not exist in project\n", serviceName)
						os.Exit(1)
					}
				}
			}

			serviceNames := filter.Apply(servicesToPush, func(s project.ServiceProject) string {
				return s.Name()
			}).([]string)

			fmt.Fprintf(out, "Pushing services: %s\n\n", strings.Join(serviceNames, ","))

			prePushscripts := kipProject.GetScripts("pre-push", o.environment)

			if len(prePushscripts) > 0 {
				for _, script := range prePushscripts {
					fmt.Fprintf(out, color.BlueString("RUN script: \"%s\"\n"), script.Name)

					err := script.Run([]string{})
					if err != nil {
						log.Fatalf("error running script \"%s\": %v", script.Name, err)
					}
				}
			}

			pushServices(out, servicesToPush, o.repository, o.key, extraArgs)

			postBuildscripts := kipProject.GetScripts("post-build", o.environment)

			if len(postBuildscripts) > 0 {
				for _, script := range postBuildscripts {
					fmt.Fprintf(out, color.BlueString("RUN script: \"%s\"\n"), script.Name)

					err := script.Run([]string{})
					if err != nil {
						log.Fatalf("error running script \"%s\": %v", script.Name, err)
					}
				}
			}
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&o.all, "all", "a", false, "build all services (default)")
	f.StringVarP(&o.environment, "environment", "e", "", "define build enviroment")
	f.StringVarP(&o.repository, "repository", "r", "", "repository to tag image with")
	f.StringVarP(&o.key, "key", "k", "latest", "key to tag latest image with")
	f.StringArrayVarP(&o.services, "service", "s", []string{}, "services to push")

	return cmd
}

func pushServices(out io.Writer, services []project.ServiceProject, repository string, key string, args []string) {
	for _, service := range services {
		if service.HasDockerfile() {
			fmt.Fprintf(out, color.BlueString("PUSH service: \"%s\"\n"), service.Name())
			pushErr := service.Push(repository, key, args)
			if pushErr == nil {
				fmt.Fprintf(out, color.BlueString("PUSH %s %s\n"), service.Name(), color.GreenString("SUCCESS"))
			} else {
				fmt.Fprint(out, pushErr)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(out, color.BlueString("SKIP service: \"%s\" no Dockerfile\n"), service.Name())
		}
	}
}
