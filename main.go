package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type groupStats struct {
	pods      int
	restarts  int32
	oomKills  int
	cpuReq    resource.Quantity
	cpuLim    resource.Quantity
	memReq    resource.Quantity
	memLim    resource.Quantity
	storage   resource.Quantity
	noLimits  int
}

func main() {
	labelFlag := flag.String("l", "", "label key to group by, or key=value to group and filter (e.g. app, app=nginx, app=nginx,team=payments)")
	namespace := flag.String("n", "", "namespace to filter (default: all namespaces)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: kubectl costby -l <label> [-n <namespace>]\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  kubectl costby -l team\n")
		fmt.Fprintf(os.Stderr, "  kubectl costby -l app=nginx\n")
		fmt.Fprintf(os.Stderr, "  kubectl costby -l app=nginx,team=payments\n")
		fmt.Fprintf(os.Stderr, "  kubectl costby -l app -n production\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *labelFlag == "" {
		flag.Usage()
		os.Exit(1)
	}

	// parse label flag: extract the group-by key and optional filters
	// e.g. "app" → groupKey=app, filters={}
	// e.g. "app=nginx" → groupKey=app, filters={app:nginx}
	// e.g. "app=nginx,team=payments" → groupKey=app (first key), filters={app:nginx, team:payments}
	labelKey, labelFilters := parseLabelFlag(*labelFlag)

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	pods, err := clientset.CoreV1().Pods(*namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing pods: %v\n", err)
		os.Exit(1)
	}

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(*namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing PVCs: %v\n", err)
		os.Exit(1)
	}

	// map PVC name+namespace to storage request
	pvcStorage := map[string]resource.Quantity{}
	for _, pvc := range pvcs.Items {
		key := pvc.Namespace + "/" + pvc.Name
		if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			pvcStorage[key] = req
		}
	}

	groups := map[string]*groupStats{}

	for _, pod := range pods.Items {
		// apply label filters — skip pod if it doesn't match all filters
		if !matchesFilters(pod.Labels, labelFilters) {
			continue
		}

		labelVal, ok := pod.Labels[labelKey]
		if !ok {
			labelVal = "<unset>"
		}

		if _, exists := groups[labelVal]; !exists {
			groups[labelVal] = &groupStats{}
		}
		g := groups[labelVal]
		g.pods++

		// container stats
		for _, c := range pod.Spec.Containers {
			if c.Resources.Limits == nil {
				g.noLimits++
			}
			if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				g.cpuReq.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
				g.cpuLim.Add(lim)
			}
			if req, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				g.memReq.Add(req)
			}
			if lim, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
				g.memLim.Add(lim)
			}
		}

		// restarts and OOMKills
		for _, cs := range pod.Status.ContainerStatuses {
			g.restarts += cs.RestartCount
			if cs.LastTerminationState.Terminated != nil &&
				cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				g.oomKills++
			}
		}

		// PVC storage via pod volumes
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				key := pod.Namespace + "/" + vol.PersistentVolumeClaim.ClaimName
				if storage, ok := pvcStorage[key]; ok {
					g.storage.Add(storage)
				}
			}
		}
	}

	// sort group keys
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "%s\tPODS\tRESTARTS\tOOMKILLS\tCPU REQ\tCPU LIM\tMEM REQ\tMEM LIM\tSTORAGE\tNO LIMITS\n",
		toUpper(labelKey))

	for _, k := range keys {
		g := groups[k]
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%d\n",
			k,
			g.pods,
			g.restarts,
			g.oomKills,
			formatCPU(g.cpuReq),
			formatCPU(g.cpuLim),
			formatMem(g.memReq),
			formatMem(g.memLim),
			formatMem(g.storage),
			g.noLimits,
		)
	}
	w.Flush()
}

func formatCPU(q resource.Quantity) string {
	if q.IsZero() {
		return "-"
	}
	// display in millicores if < 1 core, else cores
	millis := q.MilliValue()
	if millis < 1000 {
		return fmt.Sprintf("%dm", millis)
	}
	return fmt.Sprintf("%.1f", float64(millis)/1000)
}

func formatMem(q resource.Quantity) string {
	if q.IsZero() {
		return "-"
	}
	bytes := q.Value()
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMi", float64(bytes)/float64(1<<20))
	default:
		return fmt.Sprintf("%dKi", bytes/1024)
	}
}

// parseLabelFlag parses "-l app", "-l app=nginx", "-l app=nginx,team=payments"
// returns the group-by key (first key found) and a map of key→value filters
func parseLabelFlag(s string) (string, map[string]string) {
	filters := map[string]string{}
	groupKey := ""
	parts := splitComma(s)
	for i, part := range parts {
		if idx := indexOf(part, '='); idx >= 0 {
			k := part[:idx]
			v := part[idx+1:]
			filters[k] = v
			if i == 0 {
				groupKey = k
			}
		} else {
			if i == 0 {
				groupKey = part
			}
		}
	}
	return groupKey, filters
}

func matchesFilters(labels map[string]string, filters map[string]string) bool {
	for k, v := range filters {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func toUpper(s string) string {
	if len(s) == 0 {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
	return string(b)
}
