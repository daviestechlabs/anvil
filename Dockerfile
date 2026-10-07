FROM docker.io/library/golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/anvil-operator ./cmd/anvil-operator \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/anvilctl ./cmd/anvilctl
FROM scratch
COPY LICENSE /LICENSE
COPY --from=build /out/anvil-operator /anvil-operator
COPY --from=build /out/anvilctl /anvilctl
USER 65532:65532
ENTRYPOINT ["/anvil-operator"]
