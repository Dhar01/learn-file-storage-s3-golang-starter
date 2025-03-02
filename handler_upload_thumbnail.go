package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}

	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType != "image/png" && mediaType != "image/jpeg" {
		respondWithError(
			w,
			http.StatusBadRequest,
			"Unsupported media type: must be png or jpeg",
			fmt.Errorf("unsupported media type: must be image"),
		)
		return
	}

	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not read image data", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	if video.UserID != userID {
		respondWithError(
			w,
			http.StatusUnauthorized,
			"User doesn't have permission",
			fmt.Errorf("user doesn't have permission"),
		)
		return
	}

	base64ImageData := base64.StdEncoding.EncodeToString(imageData)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, base64ImageData)

	// saveThumb := thumbnail{
	// 	data:      imageData,
	// 	mediaType: mediaType,
	// }

	// videoThumbnails[videoID] = saveThumb

	// port := cfg.port
	// urlThumbnail := fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", port, videoID)

	updatedVideo := database.Video{
		ID: video.ID,
		CreateVideoParams: database.CreateVideoParams{
			Title:       video.Title,
			Description: video.Description,
			UserID:      video.UserID,
		},
		ThumbnailURL: &dataURL,
		VideoURL:     video.VideoURL,
		CreatedAt:    video.CreatedAt,
		UpdatedAt:    video.UpdatedAt,
	}

	if err := cfg.db.UpdateVideo(updatedVideo); err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't update the video data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updatedVideo)
}
