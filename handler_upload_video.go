package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	/* getting the token
	getting the userID */
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

	// getting the video metadata to see if the user is the owner of the video
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

	// parsing the uploaded video file from the form data
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse the video", err)
		return
	}

	defer file.Close()

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "video/mp4" {
		respondWithError(
			w,
			http.StatusBadRequest,
			"Unsupported media type",
			fmt.Errorf("unsupported media type: must be video/mp4"),
		)
		return
	}

	// saving the file temporary on the disk
	temp, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't create temp file", err)
		return
	}

	defer os.Remove(temp.Name())
	defer temp.Close()

	if _, err := io.Copy(temp, file); err != nil {
		respondWithError(
			w,
			http.StatusInternalServerError,
			"can't copy the contents of file to the storage file",
			err,
		)
		return
	}

	// to read the file again from the beginning
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't set the pointer to the beginning", err)
		return
	}

	fileKey := fmt.Sprintf("%x.mp4", md5.Sum([]byte(uuid.New().String())))

	// putting the video file/object to the s3 bucket
	s3Obj := &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        temp,
		ContentType: &mediaType,
	}

	if _, err := cfg.s3Client.PutObject(context.Background(), s3Obj); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload to s3", err)
		return
	}

	// videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)
	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)

	updateVideo := database.Video{
		ID: video.ID,
		CreateVideoParams: database.CreateVideoParams{
			Title:       video.Title,
			Description: video.Description,
			UserID:      video.UserID,
		},
		ThumbnailURL: video.ThumbnailURL,
		VideoURL:     &videoURL,
		CreatedAt:    video.CreatedAt,
		UpdatedAt:    video.UpdatedAt,
	}

	if err := cfg.db.UpdateVideo(updateVideo); err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't update video url", err)
		return
	}

	respondWithJSON(w, http.StatusOK, updateVideo)
}
