# hal5d
An haproxy shim for linkerd Kubernetes ingress.

[linkerd](https://linkerd.io/) can be deployed as a
[Kubernetes ingress controller](https://kubernetes.io/docs/concepts/services-networking/ingress/#ingress-controllers). 
Implementing Ingress via linkerd makes a lot of sense when linkerd also powers
your in-cluster service mesh; your ingress traffic benefits from the same
tracing, metrics, and traffic management patterns as in cluster traffic.

Unfortunately linkerd does not currently support TLS Server Name Indication
(SNI). This means your ingress controller pods cannot serve HTTPS traffic for
more than one ingress unless you use a wildcard certificate.

hal5d attempts to solve this by running a simple haproxy instance in front of
each linkerd pod. There are three components to this pattern:
* linkerd pods configured as ingress controllers.
* haproxy run via [haproxy-docker-wrapper](https://github.com/tuenti/haproxy-docker-wrapper)
* hal5d managing haproxy. 

hal5d watches a Kubernetes API server for
[TLS enabled](https://kubernetes.io/docs/concepts/services-networking/ingress/#tls) 
Kubernetes Ingress resources, saving their TLS key pairs to disk, and triggering
a haproxy reload via haproxy-docker-wrapper.