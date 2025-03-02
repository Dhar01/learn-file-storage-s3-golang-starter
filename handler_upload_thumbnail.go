package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

const (
	imagePNG = "image/png"
	imageJPEG = "image/jpeg"
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

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(
			w,
			http.StatusBadRequest,
			"can't decode the content type",
			fmt.Errorf("unsupported media type: can't decode the content type"),
		)
		return
	}

	var extension string

	switch mediaType {
	case imagePNG:
		extension = ".png"
	case imageJPEG:
		extension = ".jpeg"
	default:
		respondWithError(
			w,
			http.StatusBadRequest,
			"Unsupported media type: must be png or jpeg",
			fmt.Errorf("unsupported media type: must be image"),
		)
		return
	}

	// imageData, err := io.ReadAll(file)
	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Could not read image data", err)
	// 	return
	// }

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

	filePath := filepath.Join(cfg.assetsRoot, videoIDString+extension)
	fileStoragePath, err := os.Create(filePath)
	if err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"can't create file on the filesystem",
			fmt.Errorf("can't create file on the filesystem"),
		)
	}

	defer fileStoragePath.Close()

	if _, err := io.Copy(fileStoragePath, file); err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"can't copy the contents of file to the storage file",
			fmt.Errorf("can't copy the contents of file to the storage file"),
		)
	}

	// // will be commented
	// base64ImageData := base64.StdEncoding.EncodeToString(imageData)
	// dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, base64ImageData)

	// saveThumb := thumbnail{
	// 	data:      imageData,
	// 	mediaType: mediaType,
	// }

	// videoThumbnails[videoID] = saveThumb

	port := cfg.port
	// urlThumbnail := fmt.Sprintf("http://localhost:%s/api/thumbnails/%s", port, videoID)
	urlThumbnail := fmt.Sprintf("http://localhost:%s/assets/%s%s", port, videoID, extension)

	updatedVideo := database.Video{
		ID: video.ID,
		CreateVideoParams: database.CreateVideoParams{
			Title:       video.Title,
			Description: video.Description,
			UserID:      video.UserID,
		},
		ThumbnailURL: &urlThumbnail,
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
