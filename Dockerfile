FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY VERSION *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /surface .
RUN mkdir /results

FROM gcr.io/distroless/static-debian13:debug-nonroot@sha256:2a581fcbda6320d4d17fd6ff4774bb96e4825d2be9c2bca59f0777b429997f51
COPY --from=build /surface /usr/local/bin/surface
COPY --from=build --chown=65532:65532 /results /var/lib/surface/results
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/surface"]
CMD ["serve", "--config", "/etc/surface/config.yaml"]
