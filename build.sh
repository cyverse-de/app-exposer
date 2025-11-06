#!/bin/bash

# Script to build container images using docker or podman and output Skaffold-compatible build JSON
# Usage: ./build.sh [OPTIONS]

set -euo pipefail

# Default values
DOCKERFILE="Dockerfile"
IMAGE_NAME="harbor.cyverse.org/de/app-exposer"
TAG=""
OUTPUT_FILE="build.json"
PUSH_IMAGE=false
BUILD_CONTEXT="."
PLATFORM="linux/amd64"
RUNTIME="docker"
DOCKER_BUILD_FLAGS=""

# Function to display usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

OPTIONS:
    -d, --dockerfile PATH       Path to Dockerfile (default: Dockerfile)
    -i, --image NAME            Image name (default: harbor.cyverse.org/de/app-exposer)
    -t, --tag TAG               Image tag for building and pushing (default: latest)
    -o, --output FILE           Output JSON file path (default: build.json)
    -p, --push                  Push image after building (by tag, not digest)
    -c, --context PATH          Build context directory (default: .)
    --platform PLATFORM         Target platform (default: linux/amd64)
    --runtime RUNTIME           Container runtime: docker or podman (default: docker)
    --docker-build-flags FLAGS  Extra flags for docker build (ignored with podman)
    -h, --help                  Show this help message

Note: The build.json output uses the sha256 digest in the 'tag' field for Skaffold compatibility,
      ensuring that Kubernetes pulls the exact image that was built.

Examples:
    $0 -i harbor.cyverse.org/de/app-exposer -t v1.0.0 -o build.json
    $0 -i harbor.cyverse.org/de/app-exposer -t latest -o artifacts.json -p
    $0 -i harbor.cyverse.org/de/app-exposer -o build.json    # Uses 'latest' tag
    $0 -d custom.Dockerfile -i harbor.cyverse.org/de/app-exposer -o build.json -c /path/to/context
    $0 --runtime podman -i harbor.cyverse.org/de/app-exposer -t v1.0.0 -o build.json
    $0 --runtime docker --docker-build-flags '--network host --no-cache' -i harbor.cyverse.org/de/app-exposer -o build.json
EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--dockerfile)
            DOCKERFILE="$2"
            shift 2
            ;;
        -i|--image)
            IMAGE_NAME="$2"
            shift 2
            ;;
        -t|--tag)
            TAG="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        -p|--push)
            PUSH_IMAGE=true
            shift
            ;;
        -c|--context)
            BUILD_CONTEXT="$2"
            shift 2
            ;;
        --platform)
            PLATFORM="$2"
            shift 2
            ;;
        --runtime)
            RUNTIME="$2"
            shift 2
            ;;
        --docker-build-flags)
            DOCKER_BUILD_FLAGS="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown option $1" >&2
            usage
            exit 1
            ;;
    esac
done

# Validate runtime
if [[ "$RUNTIME" != "docker" && "$RUNTIME" != "podman" ]]; then
    echo "Error: Runtime must be 'docker' or 'podman', got: $RUNTIME" >&2
    exit 1
fi

# Set default tag if not provided
if [[ -z "$TAG" ]]; then
    TAG="latest"
fi

# Construct full image tag
FULL_IMAGE_TAG="${IMAGE_NAME}:${TAG}"

# Validate Dockerfile exists
if [[ ! -f "$BUILD_CONTEXT/$DOCKERFILE" ]]; then
    echo "Error: Dockerfile not found at $BUILD_CONTEXT/$DOCKERFILE" >&2
    exit 1
fi

# Validate build context exists
if [[ ! -d "$BUILD_CONTEXT" ]]; then
    echo "Error: Build context directory not found: $BUILD_CONTEXT" >&2
    exit 1
fi

echo "Building image: $FULL_IMAGE_TAG"
echo "Dockerfile: $BUILD_CONTEXT/$DOCKERFILE"
echo "Build context: $BUILD_CONTEXT"
echo "Platform: $PLATFORM"
echo "Runtime: $RUNTIME"

# Build the image using the selected runtime
if [[ "$RUNTIME" == "docker" ]]; then
    # Build with Docker
    if [[ -n "$DOCKER_BUILD_FLAGS" ]]; then
        echo "Docker build flags: $DOCKER_BUILD_FLAGS"
        # shellcheck disable=SC2086
        docker build \
            -t "$FULL_IMAGE_TAG" \
            -f "$BUILD_CONTEXT/$DOCKERFILE" \
            --platform "$PLATFORM" \
            $DOCKER_BUILD_FLAGS \
            "$BUILD_CONTEXT"
    else
        docker build \
            -t "$FULL_IMAGE_TAG" \
            -f "$BUILD_CONTEXT/$DOCKERFILE" \
            --platform "$PLATFORM" \
            "$BUILD_CONTEXT"
    fi
else
    # Build with Podman (ignore docker-build-flags)
    if [[ -n "$DOCKER_BUILD_FLAGS" ]]; then
        echo "Warning: --docker-build-flags ignored when using podman runtime" >&2
    fi
    podman build \
        -t "$FULL_IMAGE_TAG" \
        -f "$BUILD_CONTEXT/$DOCKERFILE" \
        --platform "$PLATFORM" \
        "$BUILD_CONTEXT"
fi

# Push the image if requested (always push by tag, not digest)
if [[ "$PUSH_IMAGE" == "true" ]]; then
    echo "Pushing image: $FULL_IMAGE_TAG"
    $RUNTIME push "$FULL_IMAGE_TAG"
fi

# Extract sha256 digest from the built image
echo "Extracting sha256 digest from built image..."
SHA256_DIGEST=$($RUNTIME inspect --format='{{index .RepoDigests 0}}' "$FULL_IMAGE_TAG" 2>/dev/null || true)

# If RepoDigests is empty (image not pushed), get the image ID instead
if [[ -z "$SHA256_DIGEST" ]]; then
    IMAGE_ID=$($RUNTIME inspect --format='{{.Id}}' "$FULL_IMAGE_TAG" | cut -d: -f2)
    if [[ -n "$IMAGE_ID" ]]; then
        SHA256_DIGEST="${IMAGE_NAME}@sha256:${IMAGE_ID}"
        echo "Using local image digest: $SHA256_DIGEST"
    else
        echo "Error: Failed to extract image digest" >&2
        exit 1
    fi
else
    echo "Using repo digest: $SHA256_DIGEST"
fi

# Create the build JSON output in Skaffold format with digest in the tag field
echo "Writing build JSON to: $OUTPUT_FILE"
cat > "$OUTPUT_FILE" << EOF
{
  "builds": [
    {
      "imageName": "$IMAGE_NAME",
      "tag": "$SHA256_DIGEST"
    }
  ]
}
EOF

echo "Build completed successfully!"
echo "Built and tagged as: $FULL_IMAGE_TAG"
echo "Image digest: $SHA256_DIGEST"
echo "Build JSON written to: $OUTPUT_FILE (using digest for Skaffold compatibility)"
