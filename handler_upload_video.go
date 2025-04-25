package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "couldn't get video", err)
		return
	}

	if video.UserID != userID {
		err := fmt.Errorf("user %d is not the owner of video %d", userID, videoID)
		respondWithError(w, http.StatusUnauthorized, "you are not authorized to access this video", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parse form file", err)
		return
	}
	defer file.Close()

	headers := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(headers)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "failed to parse content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusUnsupportedMediaType, "incorrect file type. video must be an mp4", err)
		return
	}

	// create temporary local copy of video file
	tmpVideo, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to create temp video file", err)
		return
	}
	defer tmpVideo.Close()           // Close first (LIFO)
	defer os.Remove(tmpVideo.Name()) // Then remove

	// Copy uploaded file to temporary file
	_, err = io.Copy(tmpVideo, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to write content to video file", err)
		return
	}

	// get aspect ratio
	aspectRatio, err := getVideoAspectRatio(tmpVideo.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to determine video aspect ratio", err)
		return
	}

	// determine the correct prefix
	var prefix string
	if aspectRatio == "16:9" {
		prefix = "landscape/"
	} else if aspectRatio == "9:16" {
		prefix = "portrait/"
	} else {
		prefix = "other/"
	}

	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	// reset tempFile's file pointer to the beginning
	_, err = tmpVideo.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to reset temporary video file pointer", err)
		return
	}

	// Create a unique key for S3
	assetPath := getAssetPath(mediaType)
	key := filepath.Join(prefix, assetPath)

	// Set up the S3 input parameters and upload the asset
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        tmpVideo,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	// create the new video url to point to s3 location
	url := cfg.getObjectURL(key)
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	// save new video
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to update video", err)
		return
	}

}

func getVideoAspectRatio(filePath string) (string, error) {
	type Stream struct {
		Width  int `json:"width,omitempty"`
		Height int `json:"height,omitempty"`
	}

	type FFProbeOutput struct {
		Streams []Stream `json:"streams"`
	}

	// create command to get video info
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		filePath,
	)

	// set command output to be a pointer to a new bytes.Buffer
	var out bytes.Buffer
	cmd.Stdout = &out

	// run the command
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("unable to get video data %v", err)
	}

	// unmarshal the output
	var result FFProbeOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return "", fmt.Errorf("error parsing video data %v", err)
	}

	if len(result.Streams) == 0 {
		return "", fmt.Errorf("no streams found in ffprobe output")
	}

	// get width and height of the video
	width := result.Streams[0].Width
	height := result.Streams[0].Height

	// determine the aspect ratio
	aspectRatio := getAspectCategory(width, height)

	return aspectRatio, nil
}

func getAspectCategory(width, height int) string {
	if width == 0 || height == 0 {
		return "invalid"
	}

	ratio := float64(width) / float64(height)

	const tolerance = 0.05

	switch {
	case abs(ratio-16.0/9.0) < tolerance:
		return "16:9"
	case abs(ratio-9.0/16.0) < tolerance:
		return "9:16"
	default:
		return "other"
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
