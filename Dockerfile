# syntax=docker/dockerfile:1
#
# Sprintly API — the image Cloud Run builds and runs.
#
# It lives at the repository root because Cloud Run's "continuous deployment
# from a GitHub repository" wiring looks for a Dockerfile at the root of the
# build context and offers no way to point at backend/Dockerfile. The build
# context is therefore the whole repo, and .dockerignore strips everything the
# Go binary doesn't need (frontend/, supabase/, docs, git metadata) so the
# uploaded context stays small and a frontend-only commit doesn't invalidate
# the layer cache.
#
# This is the only Dockerfile for the API — there is deliberately not a second
# one under backend/, because two copies of the same build drift apart and only
# one of them is the copy that ships.
#
#   docker build -t sprintly-api .
#   docker run --rm -p 8080:8080 --env-file .env sprintly-api

# ----------------------------------------------------------------- build
FROM golang:1.24-alpine AS build
WORKDIR /src

# Manifests first, so the module cache survives source-only changes.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# Static binary: distroless has no libc to link against.
# -s -w drops the symbol table and DWARF data (~30% smaller image).
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ----------------------------------------------------------------- runtime
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/server /app/server

# Cloud Run overrides PORT at runtime and ignores EXPOSE; this is the local and
# docker-compose default, and matches config.Load()'s fallback.
ENV PORT=8080 APP_ENV=production
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
