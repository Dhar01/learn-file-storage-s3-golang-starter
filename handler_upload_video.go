package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

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

	// process for fast start video file
	processedFilePath, err := processVideoForFastStart(temp.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't process the file", err)
		return
	}

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't open processed file", err)
		return
	}

	defer processedFile.Close()
	defer os.Remove(processedFilePath)

	aspRatio, err := getVideoAspectRatio(temp.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't get the video ration", err)
		return
	}

	// to read the file again from the beginning
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't set the pointer to the beginning", err)
		return
	}

	var ratio string

	switch aspRatio {
	case "16:9":
		ratio = "landscape"
	case "9:16":
		ratio = "portrait"
	default:
		ratio = "other"
	}

	fileKey := fmt.Sprintf("%s/%x.mp4", ratio, md5.Sum([]byte(uuid.New().String())))

	// putting the video file/object to the s3 bucket
	s3Obj := &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        processedFile,
		ContentType: &mediaType,
	}

	if _, err := cfg.s3Client.PutObject(context.Background(), s3Obj); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload to s3", err)
		return
	}

	// videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)
	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, fileKey)

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

	signedURLVideo, err := cfg.dbVideoToSignedVideo(updateVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to use signedURL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedURLVideo)
}

type Stream struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}
type FFProbeOutput struct {
	Streams []Stream `json:"streams"`
}

// getting the aspect ratio of the video file
func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	if err := cmd.Run(); err != nil {
		return "", err
	}

	var output FFProbeOutput

	if err := json.Unmarshal(stdoutBuf.Bytes(), &output); err != nil {
		return "", err
	}

	height := output.Streams[0].Height
	width := output.Streams[0].Width

	ratio := float64(width) / float64(height)

	if ratio > 1.7 && ratio < 1.9 {
		return "16:9", nil
	} else if ratio > 0.5 && ratio < 0.6 {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

// processing the video for the fast start
func processVideoForFastStart(filePath string) (string, error) {
	outFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outFilePath)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	client := s3.NewPresignClient(s3Client)
	req, err := client.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))

	if err != nil {
		return "", err
	}

	return req.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	urlParts := strings.Split(*video.VideoURL, ",")
	if len(urlParts) != 2 {
		return database.Video{}, fmt.Errorf("invalid video url format")
	}

	bucket := urlParts[0]
	key := urlParts[1]

	url, err := generatePresignedURL(cfg.s3Client, bucket, key, (time.Minute * 15))
	if err != nil {
		return database.Video{}, err
	}

	video.VideoURL = &url

	return video, nil
}
