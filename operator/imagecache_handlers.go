package operator

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// bulkImageOp binds the request, validates it, applies fn to each image,
// and returns a 200 (all ok) or 207 (partial failure) bulk response.
func (o *Operator) bulkImageOp(c echo.Context, fn func(ctx context.Context, image string) error) error {
	ctx := c.Request().Context()

	var req ImageCacheRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "invalid request body"})
	}
	if len(req.Images) == 0 {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "images list must not be empty"})
	}

	results := make([]ImageCacheResult, 0, len(req.Images))
	hasError := false
	for _, image := range req.Images {
		if err := fn(ctx, image); err != nil {
			log.Errorf("image cache operation failed for %s: %v", image, err)
			results = append(results, ImageCacheResult{Image: image, Status: "error", Error: err.Error()})
			hasError = true
		} else {
			results = append(results, ImageCacheResult{Image: image, Status: "ok"})
		}
	}

	status := http.StatusOK
	if hasError {
		status = http.StatusMultiStatus
	}
	return c.JSON(status, ImageCacheBulkResponse{Results: results})
}

// HandleCacheImages creates or updates cache DaemonSets for the given images.
//
//	@Summary		Cache container images
//	@Description	Creates a DaemonSet per image to pre-pull it onto every node.
//	@Description	Each DaemonSet uses an init container with the target image and
//	@Description	a pause main container. For distroless/scratch images lacking
//	@Description	"true", the init container will CrashLoopBackOff — this is expected
//	@Description	and the image is still cached. Status will show "cached-with-errors".
//	@Tags			image-cache
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ImageCacheRequest		true	"Images to cache"
//	@Success		200		{object}	ImageCacheBulkResponse	"All images cached successfully"
//	@Success		207		{object}	ImageCacheBulkResponse	"Partial success"
//	@Failure		400		{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [put]
func (o *Operator) HandleCacheImages(c echo.Context) error {
	return o.bulkImageOp(c, o.imageCache.EnsureImageCached)
}

// HandleRemoveCachedImages removes cache DaemonSets for the given images.
//
//	@Summary		Remove cached images (bulk)
//	@Description	Deletes the cache DaemonSets for the specified images.
//	@Description	Non-existent images are silently ignored (idempotent).
//	@Description	Note: some HTTP clients drop the body on DELETE requests.
//	@Description	Use DELETE /image-cache/:id for single-image removal from browsers.
//	@Tags			image-cache
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ImageCacheRequest		true	"Images to remove"
//	@Success		200		{object}	ImageCacheBulkResponse
//	@Success		207		{object}	ImageCacheBulkResponse	"Partial success"
//	@Failure		400		{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [delete]
func (o *Operator) HandleRemoveCachedImages(c echo.Context) error {
	return o.bulkImageOp(c, o.imageCache.RemoveCachedImage)
}

// HandleListCachedImages returns the status of all cached images.
//
//	@Summary		List cached images
//	@Description	Returns all image cache DaemonSets with their pull status.
//	@Tags			image-cache
//	@Produce		json
//	@Success		200	{object}	ImageCacheListResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [get]
func (o *Operator) HandleListCachedImages(c echo.Context) error {
	ctx := c.Request().Context()

	images, err := o.imageCache.ListCachedImages(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, ImageCacheListResponse{Images: images})
}

// HandleGetCachedImage returns the status of a single cached image.
//
//	@Summary		Get cached image status
//	@Description	Returns the cache status for a single image by its slug ID
//	@Description	(from the "id" field in list responses).
//	@Tags			image-cache
//	@Produce		json
//	@Param			id	path		string	true	"Image cache slug ID"
//	@Success		200	{object}	ImageCacheStatus
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache/{id} [get]
func (o *Operator) HandleGetCachedImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "image cache id is required"})
	}

	status, err := o.imageCache.GetCachedImageStatus(ctx, id)
	if err != nil {
		// Check if the underlying K8s error is NotFound through the wrapping.
		if apierrors.IsNotFound(err) {
			return c.JSON(http.StatusNotFound, common.ErrorResponse{Message: fmt.Sprintf("no cached image with id %q", id)})
		}
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, status)
}

// HandleDeleteCachedImage removes a single cached image by its slug ID.
//
//	@Summary		Remove a cached image
//	@Description	Deletes the cache DaemonSet for the image with the given slug ID.
//	@Description	Returns success even if already absent (idempotent).
//	@Tags			image-cache
//	@Param			id	path	string	true	"Image cache slug ID"
//	@Success		200
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache/{id} [delete]
func (o *Operator) HandleDeleteCachedImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "image cache id is required"})
	}

	if err := o.imageCache.RemoveCachedImageByID(ctx, id); err != nil {
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.NoContent(http.StatusOK)
}
