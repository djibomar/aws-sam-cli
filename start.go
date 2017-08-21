package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/awslabs/aws-sam-local/router"
	"github.com/awslabs/goformation"
	"github.com/codegangsta/cli"
)

func start(c *cli.Context) {

	// Setup the logger
	stderr := io.Writer(os.Stderr)
	logarg := c.String("log")

	if len(logarg) > 0 {
		if logFile, err := os.Create(logarg); err == nil {
			stderr = io.Writer(logFile)
			log.SetOutput(stderr)
		} else {
			log.Fatalf("Failed to open log file %s: %s\n", c.String("log"), err)
		}
	}

	filename := getTemplateFilename(c.String("template"))
	template, err := goformation.Open(filename)
	if err != nil {
		log.Fatalf("Failed to parse template: %s\n", err)
	}

	// Check connectivity to docker
	dockerVersion, err := getDockerVersion()
	if err != nil {
		log.Printf("Running AWS SAM projects locally requires Docker. Have you got it installed?\n")
		log.Printf("%s\n", err)
		os.Exit(1)
	}
	log.Printf("Connected to Docker %s", dockerVersion)

	// ...
	baseDir := c.String("docker-volume-basedir")
	checkWorkingDirExist := false
	if baseDir == "" {
		baseDir = filepath.Dir(filename)
		checkWorkingDirExist = true
	}

	// Create a new router
	mux := router.NewServerlessRouter()

	functions := template.GetAllAWSServerlessFunctionResources()

	for name, function := range functions {

		// Initiate a new Lambda runtime
		runt, err := NewRuntime(NewRuntimeOpt{
			LogicalID:            name,
			Function:             function,
			Logger:               stderr,
			EnvOverrideFile:      c.String("env-vars"),
			Basedir:              baseDir,
			CheckWorkingDirExist: checkWorkingDirExist,
			DebugPort:            c.String("debug-port"),
		})

		// Check there wasn't a problem initiating the Lambda runtime
		if err != nil {
			if err == ErrRuntimeNotSupported {
				log.Printf("Ignoring %s (%s) due to unsupported runtime (%s)\n", name, function.Handler, function.Runtime)
				continue
			} else {
				log.Printf("Ignoring %s (%s) due to %s runtime init error: %s\n", name, function.Handler, function.Runtime, err)
				continue
			}
		}

		// Add this AWS::Serverless::Function to the HTTP router
		if err := mux.AddFunction(&function, runt.InvokeHTTP()); err != nil {
			if err == router.ErrNoEventsFound {
				log.Printf("Ignoring %s (%s) as no API event sources are defined", name, function.Handler)
			}
		}

	}

	// Check we actually mounted some functions on our HTTP router
	if len(mux.Mounts()) < 1 {
		if len(functions) < 1 {
			fmt.Fprintf(stderr, "ERROR: No Serverless functions were found in your SAM template.\n")
			os.Exit(1)
		}
		fmt.Fprintf(stderr, "ERROR: None of the Serverless functions in your SAM template were able to be mounted. See above for errors.\n")
		os.Exit(1)
	}

	fmt.Fprintf(stderr, "\n")

	// Mount static files
	if c.String("static-dir") != "" {
		static, err := getWorkingDir(baseDir, c.String("static-dir"), checkWorkingDirExist)
		if err != nil {
			log.Printf("WARNING: Could not mount static files: %s\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Mounting static files from %s at /\n", static)
			mux.AddStaticDir(static, "/")
		}
	}

	for _, mount := range mux.Mounts() {
		msg := fmt.Sprintf("Mounting %s (%s) at http://%s:%s%s %s", mount.Function.Handler, mount.Function.Runtime, c.String("host"), c.String("port"), mount.Path, mount.Methods())
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}

	fmt.Fprintf(stderr, "\n")
	fmt.Fprintf(stderr, "You can now browse to the above endpoints to invoke your functions.\n")
	fmt.Fprintf(stderr, "You do not need to restart/reload SAM CLI while working on your functions,\n")
	fmt.Fprintf(stderr, "changes will be reflected instantly/automatically. You only need to restart\n")
	fmt.Fprintf(stderr, "SAM CLI if you update your AWS SAM template.\n")
	fmt.Fprintf(stderr, "\n")

	// Start the HTTP listener
	log.Fatal(http.ListenAndServe(c.String("host")+":"+c.String("port"), mux.Router()))

}
