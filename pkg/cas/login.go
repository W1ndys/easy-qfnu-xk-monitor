package cas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// 强智教务系统 URL 常量
	BaseURL      = "http://zhjw.qfnu.edu.cn/jsxsd/"
	IndexURL     = "http://zhjw.qfnu.edu.cn/jsxsd/"
	CaptchaURL   = "http://zhjw.qfnu.edu.cn/jsxsd/verifycode.servlet"
	LoginURL     = "http://zhjw.qfnu.edu.cn/jsxsd/xk/LoginToXkLdap"
	MainPageURL  = "http://zhjw.qfnu.edu.cn/jsxsd/framework/xsMain.jsp"
	SuccessMark  = "教学一体化服务平台" // 登录成功的页面标识

	// 验证码最大重试次数
	MaxCaptchaRetries = 3
)

// OCRClient 定义验证码识别客户端接口
type OCRClient interface {
	Recognize(imageData []byte) (string, error)
}

// DefaultOCRClient 默认的 OCR 客户端，调用外部 ddddocr API
type DefaultOCRClient struct {
	apiURL string
}

// NewDefaultOCRClient 创建默认 OCR 客户端
func NewDefaultOCRClient(apiURL string) *DefaultOCRClient {
	return &DefaultOCRClient{apiURL: apiURL}
}

// Recognize 调用 OCR API 识别验证码
func (c *DefaultOCRClient) Recognize(imageData []byte) (string, error) {
	// 将图片转为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// 构建请求体
	reqBody := map[string]string{
		"image": imageBase64,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("构建请求体失败: %w", err)
	}

	// 发送请求到 OCR API
	req, err := http.NewRequest("POST", c.apiURL+"/ocr/base64", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("创建 OCR 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OCR API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OCR API 返回错误状态码: %d", resp.StatusCode)
	}

	// 解析响应
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Text string `json:"text"`
		} `json:"data"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析 OCR 响应失败: %w", err)
	}

	if !result.Success {
		return "", fmt.Errorf("OCR 识别失败: %s", result.Message)
	}

	return result.Data.Text, nil
}

// LoginError 登录错误类型
type LoginError struct {
	Type    string // "captcha" 或 "password"
	Message string
}

func (e *LoginError) Error() string {
	return e.Message
}

// IsCaptchaError 判断是否为验证码错误
func IsCaptchaError(err error) bool {
	var loginErr *LoginError
	if errors.As(err, &loginErr) {
		return loginErr.Type == "captcha"
	}
	return false
}

// IsPasswordError 判断是否为密码错误
func IsPasswordError(err error) bool {
	var loginErr *LoginError
	if errors.As(err, &loginErr) {
		return loginErr.Type == "password"
	}
	return false
}

// ValidateSession 验证当前 Cookie 是否仍然有效
// 通过访问教务系统主页并检测响应页面是否包含 SuccessMark 来判断
func (c *Client) ValidateSession(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", MainPageURL, nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	return strings.Contains(string(bodyBytes), SuccessMark)
}

// LoginWithCache 优先使用缓存的 session 登录，失败后回退到完整登录流程
func (c *Client) LoginWithCache(ctx context.Context, username, password string, ocrClient OCRClient) error {
	// 1. 尝试加载本地 session
	if c.LoadSession() {
		log.Println("[INFO] 正在验证缓存的会话...")
		if c.ValidateSession(ctx) {
			log.Println("[INFO] 缓存会话有效，跳过登录流程")
			return nil
		}
		log.Println("[INFO] 缓存会话已失效，执行完整登录流程")
		// 重置为干净的 jar，避免旧 Cookie 干扰新的登录流程
		c.ResetJar()
	}

	// 2. 执行完整登录
	if err := c.Login(ctx, username, password, ocrClient); err != nil {
		return err
	}

	// 3. 登录成功后保存 session
	if err := c.SaveSession(); err != nil {
		log.Printf("[WARN] 保存 session 失败: %v", err)
	}

	return nil
}

// Login 执行强智教务系统的登录流程
func (c *Client) Login(ctx context.Context, username, password string, ocrClient OCRClient) error {
	// 1. 访问首页获取初始 Cookie
	if err := c.visitIndex(ctx); err != nil {
		return err
	}

	// 2. 登录循环（最多重试 MaxCaptchaRetries 次）
	var lastErr error
	for attempt := 1; attempt <= MaxCaptchaRetries; attempt++ {
		log.Printf("[INFO] 登录尝试 %d/%d", attempt, MaxCaptchaRetries)

		err := c.loginAttempt(ctx, username, password, ocrClient)
		if err == nil {
			// 登录成功
			return nil
		}

		lastErr = err

		// 如果是密码错误，直接返回，不需要重试
		if IsPasswordError(err) {
			return err
		}

		// 如果是验证码错误，继续重试
		if IsCaptchaError(err) {
			log.Printf("[WARN] 验证码错误，准备重试")
			// 短暂延迟后重试
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// 其他错误直接返回
		return err
	}

	return fmt.Errorf("登录失败，已达到最大重试次数: %w", lastErr)
}

// visitIndex 访问首页获取初始 Cookie
func (c *Client) visitIndex(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", IndexURL, nil)
	if err != nil {
		return fmt.Errorf("创建首页请求失败: %w", err)
	}

	// 设置浏览器请求头
	c.setBrowserHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("访问首页失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("访问首页异常: %d", resp.StatusCode)
	}

	log.Println("[INFO] 已访问首页，获取初始 Cookie")
	return nil
}

// loginAttempt 执行一次登录尝试
func (c *Client) loginAttempt(ctx context.Context, username, password string, ocrClient OCRClient) error {
	// 1. 获取验证码图片
	captchaData, err := c.getCaptcha(ctx)
	if err != nil {
		return err
	}

	// 2. 识别验证码
	captchaText, err := ocrClient.Recognize(captchaData)
	if err != nil {
		return fmt.Errorf("验证码识别失败: %w", err)
	}
	log.Printf("[DEBUG] 验证码识别结果: %s", captchaText)

	// 3. 生成 encoded 字符串
	encoded := generateEncoded(username, password)

	// 4. 提交登录请求
	respBody, err := c.submitLogin(ctx, captchaText, encoded)
	if err != nil {
		return err
	}

	// 5. 判断登录结果
	return c.checkLoginResult(respBody)
}

// getCaptcha 获取验证码图片
func (c *Client) getCaptcha(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", CaptchaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建验证码请求失败: %w", err)
	}

	c.setBrowserHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取验证码失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取验证码异常: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取验证码数据失败: %w", err)
	}

	log.Println("[INFO] 已获取验证码图片")
	return data, nil
}

// generateEncoded 生成 encoded 字符串
// 格式: Base64(用户名) + "%%%" + Base64(密码)
func generateEncoded(username, password string) string {
	usernameBase64 := base64.StdEncoding.EncodeToString([]byte(username))
	passwordBase64 := base64.StdEncoding.EncodeToString([]byte(password))
	return usernameBase64 + "%%%" + passwordBase64
}

// submitLogin 提交登录请求
func (c *Client) submitLogin(ctx context.Context, captchaText, encoded string) (string, error) {
	// 构建表单数据
	formData := url.Values{
		"userAccount":   {""},
		"userPassword":  {""},
		"RANDOMCODE":    {captchaText},
		"encoded":       {encoded},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", LoginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("创建登录请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://zhjw.qfnu.edu.cn")
	req.Header.Set("Referer", "http://zhjw.qfnu.edu.cn/")
	c.setBrowserHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("提交登录请求失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取登录响应失败: %w", err)
	}

	return string(bodyBytes), nil
}

// checkLoginResult 检查登录结果
func (c *Client) checkLoginResult(respBody string) error {
	if strings.Contains(respBody, "验证码错误") {
		return &LoginError{Type: "captcha", Message: "验证码错误"}
	}
	if strings.Contains(respBody, "密码错误") {
		return &LoginError{Type: "password", Message: "用户名或密码错误"}
	}

	// 验证登录状态
	req, err := http.NewRequestWithContext(context.Background(), "GET", MainPageURL, nil)
	if err != nil {
		return fmt.Errorf("创建验证请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("验证登录状态失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(bodyBytes), SuccessMark) {
		return errors.New("登录流程结束，但未检测到登录成功标识")
	}

	log.Println("[INFO] 检测到登录成功标识，登录流程完成")
	return nil
}

// setBrowserHeaders 设置浏览器请求头
func (c *Client) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Connection", "keep-alive")
}
