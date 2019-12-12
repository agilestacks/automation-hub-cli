package cmd

import (
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"hub/api"
	"hub/config"
	"hub/util"
)

var (
	autoCreateTemplate bool
	createNewTemplate  bool
	knownImportKinds   = []string{"k8s-aws", "eks", "gke", "aks", "metal", "hybrid", "openshift"}
	importRegion       string
	k8sEndpoint        string
	eksClusterName     string
	eksEndpoint        string
	gkeClusterName     string
	aksClusterName     string
	azureResourceGroup string
	metalEndpoint      string
	metalIngress       string
	bearerToken        string
)

var importCmd = &cobra.Command{
	Use: fmt.Sprintf("import <%s> <name or FQDN> -e <id | environment name> [-m <id | template name>] < keys.pem",
		strings.Join(knownImportKinds, " | ")),
	Short: "Import Kubernetes cluster",
	Long: `Import Kubernetes cluster into SuperHub to become Platform Stack.

Currently supported cluster types are:
- k8s-aws - AgileStacks Kubernetes on AWS (stack-k8s-aws)
- eks - AWS EKS
- gke - GCP GKE
- aks - Azure AKS
- metal - Bare-metal
- hybrid - Hybrid bare-metal
- openshift - OpenShift on AWS

Cluster TLS auth is read from stdin in the order:
- k8s-aws, hybrid, metal - Client cert, Client key, CA cert (optional).
- eks - CA cert, optional if --eks-endpoint is omited, then it will be discovered via AWS API
- openshift - optional CA cert
GKE and AKS certificates are discovered by import adapter component.

User-supplied FQDN must match Cloud Account's base domain.
If no FQDN is supplied, then the name is prepended to Environment's Cloud Account base domain name
to construct FQDN.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return importKubernetes(args)
	},
}

var maybeValidHostname = regexp.MustCompile("^[0-9a-z\\.-]+$")

func importKubernetes(args []string) error {
	if len(args) != 2 {
		return errors.New("Import command has two argument - type of imported Kubernetes cluster and desired cluster name")
	}
	kind := args[0]
	name := strings.ToLower(args[1])

	if !util.Contains(knownImportKinds, kind) {
		return fmt.Errorf("Kubernetes cluster kind must be one of %v", knownImportKinds)
	}

	nativeEndpoint := ""
	nativeClusterName := ""
	switch kind {
	case "k8s-aws":
		if k8sEndpoint == "" {
			return errors.New("AgileStacks K8S cluster API endpoint must be specified by --k8s-endpoint")
		}
		nativeEndpoint = k8sEndpoint

	case "hybrid":
		if metalEndpoint == "" {
			return errors.New("Hybrid bare-metal cluster API endpoint must be specified by --metal-endpoint")
		}
		nativeEndpoint = metalEndpoint

	case "metal":
		if metalEndpoint == "" {
			return errors.New("Bare-metal cluster API endpoint must be specified by --metal-endpoint")
		}
		nativeEndpoint = metalEndpoint

	case "openshift":
		if bearerToken == "" {
			return errors.New("OpenShift authentication must be specified with --bearer-token")
		}

	case "eks":
		if eksClusterName == "" {
			if strings.Contains(name, ".") {
				return errors.New("EKS cluster name (--eks-cluster) must be provided")
			} else {
				log.Printf("Setting --eks-cluster=%s", name)
				eksClusterName = name
			}
		}
		nativeEndpoint = eksEndpoint
		nativeClusterName = eksClusterName

	case "gke":
		if gkeClusterName == "" {
			if strings.Contains(name, ".") {
				return errors.New("GKE cluster name (--gke-cluster) must be provided")
			} else {
				log.Printf("Setting --gke-cluster=%s", name)
				gkeClusterName = name
			}
		}
		nativeClusterName = gkeClusterName

	case "aks":
		if aksClusterName == "" {
			if strings.Contains(name, ".") {
				return errors.New("AKS cluster name (--aks-cluster) must be provided")
			} else {
				log.Printf("Setting --aks-cluster=%s", name)
				aksClusterName = name
			}
		}
		nativeClusterName = aksClusterName
		if azureResourceGroup == "" {
			log.Printf("Azure resource group name (--azure-resource-group) not be provided - using default Cloud Account resource group")
		}
	}
	if len(nativeEndpoint) >= 8 && strings.HasPrefix(nativeEndpoint, "https://") {
		nativeEndpoint = nativeEndpoint[8:]
	}

	if !maybeValidHostname.MatchString(name) {
		return fmt.Errorf("`%s` doesn't look like a valid hostname", name)
	}

	if environmentSelector == "" {
		return errors.New("Environment name or id must be specified by --environment / -e")
	}

	// TODO review interaction of these options
	// if templateSelector != "" {
	// 	autoCreateTemplate = false
	// }

	// if createNewTemplate && templateSelector != "" {
	// 	return fmt.Errorf("If --template is specified then omit --create-new-template")
	// }

	if dryRun {
		waitAndTailDeployLogs = false
	}

	config.AggWarnings = false // confusing UIX otherwise

	api.ImportKubernetes(kind, name, environmentSelector, templateSelector,
		autoCreateTemplate, createNewTemplate, waitAndTailDeployLogs, dryRun,
		os.Stdin, bearerToken,
		importRegion, nativeEndpoint, nativeClusterName,
		metalIngress, azureResourceGroup)

	return nil
}

func init() {
	importCmd.Flags().StringVarP(&environmentSelector, "environment", "e", "",
		"Put cluster in Environment, supply name or id")
	importCmd.Flags().StringVarP(&templateSelector, "template", "m", "",
		"Use specified adapter template, by name or id")
	importCmd.Flags().StringVarP(&importRegion, "region", "", "",
		"Cloud region if different from Cloud Account region")
	importCmd.Flags().StringVarP(&k8sEndpoint, "k8s-endpoint", "", "",
		"AgileStacks Kubernetes cluster API endpoint, default to api.{domain}")
	importCmd.Flags().StringVarP(&eksClusterName, "eks-cluster", "", "",
		"AWS EKS cluster native name")
	importCmd.Flags().StringVarP(&eksEndpoint, "eks-endpoint", "", "",
		"AWS EKS cluster API endpoint (discovered via AWS EKS API if cluster name is supplied)")
	importCmd.Flags().StringVarP(&gkeClusterName, "gke-cluster", "", "",
		"GCP GKE cluster native name")
	importCmd.Flags().StringVarP(&aksClusterName, "aks-cluster", "", "",
		"Azure AKS cluster native name")
	importCmd.Flags().StringVarP(&azureResourceGroup, "azure-resource-group", "", "",
		"Azure resource group name")
	importCmd.Flags().StringVarP(&metalEndpoint, "metal-endpoint", "", "",
		"Bare-metal cluster Kubernetes API endpoint (IP or hostname [:port])")
	importCmd.Flags().StringVarP(&metalIngress, "metal-ingress", "", "",
		"Bare-metal cluster static ingress (IP or hostname, default to IP or hostname of the API endpoint)")
	importCmd.Flags().StringVarP(&bearerToken, "bearer-token", "b", "",
		"Use Bearer token to authenticate (to the OpenShift cluster)")
	importCmd.Flags().BoolVarP(&autoCreateTemplate, "create-template", "", true,
		"Create adapter template if no existing template is found for reuse")
	importCmd.Flags().BoolVarP(&createNewTemplate, "create-new-template", "", false,
		"Do not reuse existing template, always create fresh one")
	importCmd.Flags().BoolVarP(&waitAndTailDeployLogs, "wait", "w", false,
		"Wait for deployment and tail logs")
	importCmd.Flags().BoolVarP(&dryRun, "dry", "y", false,
		"Save parameters and envrc to Template's Git but do not start the import")
	apiCmd.AddCommand(importCmd)
}
