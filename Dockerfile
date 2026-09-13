FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY lua ./lua
ARG APP=ai-gateway
RUN test "$APP" = "ai-gateway" -o "$APP" = "mockprovider"
RUN if [ "$APP" = "ai-gateway" ]; then package="./cmd"; else package="./cmd/$APP"; fi && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app "$package"

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
COPY --from=build /src/lua /lua
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/app"]
