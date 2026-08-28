FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG APP
RUN test "$APP" = "gateway" -o "$APP" = "backend"
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app "./cmd/${APP}"

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
