# kubectl-costby

A kubectl plugin that shows resource consumption grouped by any Kubernetes label.
No external dependencies. Works on any cluster.

## Installation

### Manual

```bash
go install github.com/vinayus/kubectl-costby@latest
mv $(go env GOPATH)/bin/kubectl-costby /usr/local/bin/
```

### Build from source

```bash
git clone https://github.com/vinayus/kubectl-costby.git
cd kubectl-costby
go build -o kubectl-costby .
mv kubectl-costby /usr/local/bin/
```

## Usage

```bash
kubectl costby -l <label>
```

### Examples

```bash
# Group all pods by the 'team' label
kubectl costby -l team

# Group all pods by the 'app' label in a specific namespace
kubectl costby -l app -n production

# Filter to a specific label value
kubectl costby -l app=nginx

# Filter by multiple labels (groups by the first key)
kubectl costby -l app=nginx,team=payments
```

### Output

```
APP       PODS   RESTARTS   OOMKILLS   CPU REQ   CPU LIM   MEM REQ   MEM LIM   STORAGE   NO LIMITS
api       4      12         0          4.0       8.0       8.0Gi     16.0Gi    -         0
worker    20     280        6          24.0      48.0      96.0Gi    192.0Gi   1.5Ti     2
<unset>   2      0          0          -         -         -         -         -         2
```

| Column | Description |
|--------|-------------|
| PODS | Number of pods in the group |
| RESTARTS | Total restart count across all containers |
| OOMKILLS | Containers terminated due to OOM in last termination |
| CPU REQ | Sum of CPU requests |
| CPU LIM | Sum of CPU limits |
| MEM REQ | Sum of memory requests |
| MEM LIM | Sum of memory limits |
| STORAGE | Sum of PVC storage requests mounted by pods in the group |
| NO LIMITS | Containers with no resource limits set |

Pods missing the label are grouped under `<unset>`.

## Requirements

- `kubectl` configured with a valid kubeconfig
- Go 1.21+ (to build from source)
