kubectl get pods -n knative-serving -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].name}{"\n"}{end}' | grep activator | awk '{print $1}' | xargs -I {} kubectl logs -f {} -n knative-serving