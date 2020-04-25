package fileupload

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/kennygrant/sanitize"
)

type File struct {
	FileName string `json:"FileName"`
	URL      string `json:"URL"`
}

func FromRequestToFile(req *http.Request, path string) (string, string, error) {
	req.ParseMultipartForm(32)
	file, handler, err := req.FormFile("file")
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	filename := getValidFileName(path, handler.Filename)
	fullpath := path + filename
	f, err := os.OpenFile(fullpath, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	_, err = io.Copy(f, file)
	if err != nil {
		return "", "", err
	}
	return filename, fullpath, nil
}

func FromBuffer(name string, path string, body io.Reader) (string, string, error) {
	filename := getValidFileName(path, name)
	return FromBufferNoSanitize(filename, path, body)
}

func FromBufferNoSanitize(name string, path string, body io.Reader) (string, string, error) {
	// filename := getValidFileName(path, name)
	fullpath := path + name
	f, err := os.OpenFile(fullpath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	// we read from tee reader as it hasn't already done its scan
	_, err = io.Copy(f, body)
	if err != nil {
		return "", "", err
	}
	return name, fullpath, nil
}

func getValidFileName(path string, filename string) string {
	return getValidFileNameWithDupIndex(path, filename, 0)
}

func getValidFileNameWithDupIndex(path string, filename string, duplicateIndex int) string {
	filename = sanitize.Path(filename)
	dupStr := ""
	if duplicateIndex > 0 {
		dupStr = "" + strconv.Itoa(duplicateIndex) + "-"
	}
	fullpath := path + dupStr + filename

	// path doesn't exist so we can return this path
	if _, err := os.Stat(fullpath); os.IsNotExist(err) {
		return dupStr + filename
	}

	//otherwise increase file index and
	duplicateIndex++
	return getValidFileNameWithDupIndex(path, filename, duplicateIndex)
}

type operation struct {
	Operation string                 `json:"operation"`
	Params    map[string]interface{} `json:"params"`
}

type operations struct {
	Ops []*operation
}

func (o *operation) addParam(key string, val interface{}) {
	if o.Params == nil {
		o.Params = make(map[string]interface{})
	}
	o.Params[key] = val
}

func (o *operations) add(op string) {
	if o.Ops == nil {
		o.Ops = make([]*operation, 0)
	}
	o.Ops = append(o.Ops, &operation{
		Operation: op,
	})
}

func (o *operations) last() *operation {
	return o.Ops[len(o.Ops)-1]
}

func ProcessedImageFromRequest(req *http.Request, imageType string, width int, height int, quality int, convert bool) ([]byte, error) {
	err := req.ParseMultipartForm(32)
	if err != nil {
		return nil, err
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return ProcessedImage(file, imageType, width, height, quality, convert)
}

func ProcessedImage(r io.Reader, imageType string, width int, height int, quality int, convert bool) ([]byte, error) {
	ops := &operations{}

	originalImageType := "jpg"
	if convert {
		ops.add("convert")

		// converting
		if imageType == "jpg" {
			imageType = "jpeg"
			originalImageType = "png"
		} else if imageType == "png" {
			originalImageType = "jpg"
		}
		ops.last().addParam("type", imageType)
	}

	ops.add("fit")
	ops.last().addParam("width", width)    //absolute max
	ops.last().addParam("height", height)  // dont need its ratio based
	ops.last().addParam("stripmeta", true) // dont need its ratio based
	ops.last().addParam("quality", quality)
	// ops.last().addParam("compression", quality)
	bOps, err := json.Marshal(ops.Ops)
	if err != nil {
		return nil, err
	}
	endpoint := os.Getenv("IMAGE_PROCESSING_ENDPOINT") + "pipeline?operations=" + url.QueryEscape(string(bOps))

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", "filename_placeholder."+originalImageType)
	if err != nil {
		return nil, err
		// ctx.ErrorJSON(http.StatusOK, "couldn't create form file ", err)
	}
	_, err = io.Copy(fw, r)
	if err != nil {
		// ctx.ErrorJSON(http.StatusOK, "failed to copy from reqFile", err)
		return nil, err
	}
	err = w.Close()
	if err != nil {
		// ctx.ErrorJSON(http.StatusOK, "failed to copy from reqFile", err)
		return nil, err
	}

	req, err := http.NewRequest("POST", endpoint, &b)
	if err != nil {
		// ctx.ErrorJSON(http.StatusOK, "failed to copy from reqFile", err)
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil || res.StatusCode != 200 {
		// ctx.ErrorJSON(http.StatusInternalServerError, "bad request", err)
		return nil, err
	}
	defer res.Body.Close()

	var finalBts bytes.Buffer
	wr := bufio.NewWriter(&finalBts)
	// we read from tee reader as it hasn't already done its scan
	_, err = io.Copy(wr, res.Body)
	if err != nil {
		// ctx.ErrorJSON(http.StatusInternalServerError, "Failed to create image", err)
		return nil, err
	}

	return finalBts.Bytes(), nil
}

func FromBytes(name string, path string, b []byte) (string, string, error) {
	return FromBuffer(name, path, bytes.NewReader(b))
}

func FromBytesNoSanitize(name string, path string, b []byte) (string, string, error) {
	return FromBufferNoSanitize(name, path, bytes.NewReader(b))
}

func DownloadFile(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, errors.New("download failed for " + url)
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return b, err
}

func DownloadToFile(url string, filename string, filepath string) (string, string, error) {
	b, err := DownloadFile(url)
	if err != nil {
		return "", "", err
	}
	return FromBytes(filename, filepath, b)
}

func DownloadToFileNoSanitize(url string, filename string, filepath string) (string, string, error) {
	b, err := DownloadFile(url)
	if err != nil {
		return "", "", err
	}
	return FromBytesNoSanitize(filename, filepath, b)
}
