FROM golang:1.21 as builder

ENV GO111MODULE=on
ENV CGO_ENABLED=0

COPY / /work
WORKDIR /work
RUN make

FROM scratch
COPY --from=builder /work/bin/gardener-slacker /gardener-slacker
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER 65535
ENTRYPOINT ["/gardener-slacker","check"]
