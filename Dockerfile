# syntax=docker/dockerfile:1

# The frontend is built first and copied into the final image, so one container
# serves both the API and the app from a single origin. No CORS, no second
# service, no reverse proxy to configure.
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Released binaries are built with a current toolchain for its security fixes.
# The module's floor stays at 1.22 and CI tests against it, so this choice
# never restricts who can build the project.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
# Cache mounts keep rebuilds fast without baking the module cache into a layer.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nusa ./cmd/nusa

# Distroless: no shell, no package manager, non-root by default. Migrations are
# embedded in the binary, so nothing else needs to come along.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/nusa /app/nusa
COPY --from=web /web/dist /app/web/dist
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/nusa"]
CMD ["serve"]
