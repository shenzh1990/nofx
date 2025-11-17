// okx_trader.go
package trader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OkxTrader 实现 Trader 接口，面向 OKX V5 永续合约 (SWAP)
// 注：OKX 的 instId 格式通常为 "BTC-USDT-SWAP"。如果用户传入 "BTC-USDT" 或 "BTCUSDT"，
// 本实现会尽量转换为 "BTC-USDT-SWAP"。
//
// 参考：OKX V5 API 文档（签名与接口路径）。:contentReference[oaicite:1]{index=1}
type OkxTrader struct {
	apiKey     string
	secret     string
	passphrase string
	baseURL    string // e.g. https://www.okx.com

	httpClient *http.Client

	// 余额缓存
	cachedBalance     map[string]interface{}
	balanceCacheTime  time.Time
	balanceCacheMutex sync.RWMutex

	// 持仓缓存
	cachedPositions     []map[string]interface{}
	positionsCacheTime  time.Time
	positionsCacheMutex sync.RWMutex

	// 缓存时长
	cacheDuration time.Duration
}

// NewOkxTrader 创建 OKX 交易器
// baseURL 示例: "https://www.okx.com"
func NewOkxTrader(apiKey, secret, passphrase, baseURL string) *OkxTrader {
	if baseURL == "" {
		baseURL = "https://www.okx.com"
	}
	return &OkxTrader{
		apiKey:        apiKey,
		secret:        secret,
		passphrase:    passphrase,
		baseURL:       strings.TrimRight(baseURL, "/"),
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		cacheDuration: 15 * time.Second,
	}
}

// --- 辅助：签名 & HTTP 请求 ---
//
// OKX V5 签名规则：
//
//	preHash = timestamp + method + requestPath + body
//	signature = base64( HMAC_SHA256(preHash, secret) )
//
// 见 OKX API 文档说明。:contentReference[oaicite:2]{index=2}
func (t *OkxTrader) sign(timestamp, method, requestPath string, body []byte) string {
	pre := timestamp + strings.ToUpper(method) + requestPath
	if body != nil && len(body) > 0 {
		pre += string(body)
	}
	mac := hmac.New(sha256.New, []byte(t.secret))
	mac.Write([]byte(pre))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (t *OkxTrader) doRequest(ctx context.Context, method, path string, body interface{}, auth bool, query map[string]string) ([]byte, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	// 构造 URL
	u := t.baseURL + path
	if len(query) > 0 {
		v := url.Values{}
		for k, val := range query {
			v.Set(k, val)
		}
		u = u + "?" + v.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	if auth {
		ts := strconv.FormatFloat(float64(time.Now().UTC().UnixNano())/1e9, 'f', -1, 64) // seconds with fraction
		// requestPath must include query part if present: path + ?a=b
		requestPath := path
		if len(query) > 0 {
			v := url.Values{}
			for k, val := range query {
				v.Set(k, val)
			}
			requestPath = requestPath + "?" + v.Encode()
		}
		sign := t.sign(ts, method, requestPath, bodyBytes)
		req.Header.Set("OK-ACCESS-KEY", t.apiKey)
		req.Header.Set("OK-ACCESS-SIGN", sign)
		req.Header.Set("OK-ACCESS-TIMESTAMP", ts)
		req.Header.Set("OK-ACCESS-PASSPHRASE", t.passphrase)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// OKX 在非 2xx 时仍会返回 body 包含 code/msg，尽量把它包含在错误中
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("okx http %d: %s", resp.StatusCode, string(respBytes))
	}
	return respBytes, nil
}

// --- 接口实现 ---
// GetBalance 获取账户余额（带缓存）
// 使用 endpoint: GET /api/v5/account/balance
func (t *OkxTrader) GetBalance() (map[string]interface{}, error) {
	// 检查缓存
	t.balanceCacheMutex.RLock()
	if t.cachedBalance != nil && time.Since(t.balanceCacheTime) < t.cacheDuration {
		age := time.Since(t.balanceCacheTime)
		t.balanceCacheMutex.RUnlock()
		log.Printf("✓ 使用缓存的 OKX 余额（%.1fs 前）", age.Seconds())
		return t.cachedBalance, nil
	}
	t.balanceCacheMutex.RUnlock()

	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/account/balance", nil, true, nil)
	if err != nil {
		return nil, fmt.Errorf("获取 OKX 余额失败: %w", err)
	}

	// 解析响应
	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, err
	}
	// OKX 返回结构: { "code":"0", "data":[{...}], "msg":"" }
	result := make(map[string]interface{})
	// 将 data 列表中各币种余额汇总为 map
	if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
		// data 里面每项包含 "details": [ { "ccy":"USDT","availBal":"..." ... } ]
		for _, item := range data {
			if m, ok := item.(map[string]interface{}); ok {
				if details, ok := m["details"].([]interface{}); ok {
					for _, d := range details {
						if dd, ok := d.(map[string]interface{}); ok {
							ccy := ""
							if v, ok := dd["ccy"].(string); ok {
								ccy = v
							}
							avail := 0.0
							if v, ok := dd["availBal"].(string); ok {
								if fv, e := strconv.ParseFloat(v, 64); e == nil {
									avail = fv
								}
							}
							total := 0.0
							if v, ok := dd["bal"].(string); ok {
								if fv, e := strconv.ParseFloat(v, 64); e == nil {
									total = fv
								}
							}
							result[ccy] = map[string]float64{
								"available": avail,
								"total":     total,
							}
						}
					}
				}
			}
		}
	}

	// 更新缓存
	t.balanceCacheMutex.Lock()
	t.cachedBalance = make(map[string]interface{})
	for k, v := range result {
		t.cachedBalance[k] = v
	}
	t.balanceCacheTime = time.Now()
	t.balanceCacheMutex.Unlock()

	return t.cachedBalance, nil
}

// GetPositions 获取持仓（带缓存）
// 使用 endpoint: GET /api/v5/account/positions?instType=SWAP
func (t *OkxTrader) GetPositions() ([]map[string]interface{}, error) {
	// 检查缓存
	t.positionsCacheMutex.RLock()
	if t.cachedPositions != nil && time.Since(t.positionsCacheTime) < t.cacheDuration {
		age := time.Since(t.positionsCacheTime)
		t.positionsCacheMutex.RUnlock()
		log.Printf("✓ 使用缓存的 OKX 持仓（%.1fs 前）", age.Seconds())
		return t.cachedPositions, nil
	}
	t.positionsCacheMutex.RUnlock()

	ctx := context.Background()
	query := map[string]string{"instType": "SWAP"}
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/account/positions", nil, true, query)
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	if data, ok := raw["data"].([]interface{}); ok {
		for _, item := range data {
			if m, ok := item.(map[string]interface{}); ok {
				// 每个 m 代表一个持仓
				symbol := ""
				if v, ok := m["instId"].(string); ok {
					symbol = v
				}
				pos := 0.0
				if v, ok := m["pos"].(string); ok {
					if fv, e := strconv.ParseFloat(v, 64); e == nil {
						pos = fv
					}
				}
				entry := 0.0
				if v, ok := m["avgPx"].(string); ok {
					if fv, e := strconv.ParseFloat(v, 64); e == nil {
						entry = fv
					}
				}

				if v, ok := m["liab"].(string); ok {
					_ = v
				}
				unReal := 0.0
				if v, ok := m["upl"].(string); ok {
					if fv, e := strconv.ParseFloat(v, 64); e == nil {
						unReal = fv
					}
				}
				leverage := 0.0
				if v, ok := m["lever"].(string); ok {
					if fv, e := strconv.ParseFloat(v, 64); e == nil {
						leverage = fv
					}
				}

				side := "long"
				if pos < 0 {
					side = "short"
				}
				pm := map[string]interface{}{}
				pm["symbol"] = symbol
				pm["positionAmt"] = pos
				pm["entryPrice"] = entry
				pm["unRealizedProfit"] = unReal
				pm["leverage"] = leverage
				pm["side"] = side
				pm["raw"] = m
				result = append(result, pm)
			}
		}
	}

	// 更新缓存
	t.positionsCacheMutex.Lock()
	t.cachedPositions = result
	t.positionsCacheTime = time.Now()
	t.positionsCacheMutex.Unlock()

	return result, nil
}

// helper: ensure OKX instId 格式（xxx-USDT-SWAP）
func (t *OkxTrader) normalizeSymbol(symbol string) string {
	if strings.Contains(symbol, "SWAP") || strings.Contains(symbol, "PERPETUAL") {
		return symbol
	}
	// 常见用户会传入 BTC-USDT 或 BTCUSDT 或 BTC/USDT
	// 先替换 '/'
	s := strings.ReplaceAll(symbol, "/", "-")
	// 如果已经包含 '-' 且后缀为 USDT 之类，则追加 -SWAP
	parts := strings.Split(s, "-")
	if len(parts) >= 2 {
		// 如果最后部分是 USDT 或 USD 或 USDC，append -SWAP
		last := parts[len(parts)-1]
		if strings.EqualFold(last, "USDT") || strings.EqualFold(last, "USD") || strings.EqualFold(last, "USDC") {
			return s + "-SWAP"
		}
	}
	// 兜底：直接 append -USDT-SWAP
	return s + "-USDT-SWAP"
}

// OpenLong 开多仓（市价）
// 使用 POST /api/v5/trade/order
func (t *OkxTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 先取消挂单
	_ = t.CancelAllOrders(symbol)

	// 格式化数量
	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}
	qf, _ := strconv.ParseFloat(qstr, 64)
	if qf <= 0 {
		return nil, fmt.Errorf("开仓数量过小或格式化为0")
	}

	instId := t.normalizeSymbol(symbol)

	// 组装 body
	body := map[string]interface{}{
		"instId":  instId,
		"tdMode":  "cross", // 默认为全仓；如果需要逐仓可用 SetMarginMode 调整
		"side":    "buy",
		"ordType": "market",
		"sz":      qstr,
		"posSide": "long",
	}

	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return nil, fmt.Errorf("开多仓失败: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, err
	}
	result := map[string]interface{}{"raw": raw}
	// 尽量把 orderId / ordId 等放回
	if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
		if d0, ok := data[0].(map[string]interface{}); ok {
			if ordId, ok := d0["ordId"].(string); ok {
				result["orderId"] = ordId
			}
			if instId, ok := d0["instId"].(string); ok {
				result["symbol"] = instId
			}
			if s, ok := d0["sCode"].(string); ok {
				result["statusCode"] = s
			}
		}
	}

	log.Printf("✓ OKX 开多仓 %s 数量=%s", instId, qstr)
	return result, nil
}

// OpenShort 开空仓（市价）
func (t *OkxTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	_ = t.CancelAllOrders(symbol)

	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}
	qf, _ := strconv.ParseFloat(qstr, 64)
	if qf <= 0 {
		return nil, fmt.Errorf("开仓数量过小或格式化为0")
	}

	instId := t.normalizeSymbol(symbol)

	body := map[string]interface{}{
		"instId":  instId,
		"tdMode":  "cross",
		"side":    "sell",
		"ordType": "market",
		"sz":      qstr,
		"posSide": "short",
	}

	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return nil, fmt.Errorf("开空仓失败: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, err
	}
	result := map[string]interface{}{"raw": raw}
	if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
		if d0, ok := data[0].(map[string]interface{}); ok {
			if ordId, ok := d0["ordId"].(string); ok {
				result["orderId"] = ordId
			}
			if instId, ok := d0["instId"].(string); ok {
				result["symbol"] = instId
			}
		}
	}

	log.Printf("✓ OKX 开空仓 %s 数量=%s", instId, qstr)
	return result, nil
}

// CloseLong 平多仓（market buy 为平多时用 sell）
// quantity=0 表示全部
func (t *OkxTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	if quantity == 0 {
		// 查询持仓
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}
		for _, p := range positions {
			if p["symbol"] == t.normalizeSymbol(symbol) && p["side"] == "long" {
				if v, ok := p["positionAmt"].(float64); ok {
					quantity = v
				}
			}
		}
		if quantity == 0 {
			return nil, fmt.Errorf("未找到 %s 的多仓", symbol)
		}
	}

	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}
	instId := t.normalizeSymbol(symbol)
	body := map[string]interface{}{
		"instId":     instId,
		"tdMode":     "cross",
		"side":       "sell",
		"ordType":    "market",
		"sz":         qstr,
		"posSide":    "long",
		"reduceOnly": true,
	}

	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return nil, fmt.Errorf("平多仓失败: %w", err)
	}
	var raw map[string]interface{}
	_ = json.Unmarshal(respBytes, &raw)
	log.Printf("✓ OKX 平多仓 %s 数量=%s", instId, qstr)
	return map[string]interface{}{"raw": raw}, nil
}

// CloseShort 平空仓
func (t *OkxTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}
		for _, p := range positions {
			if p["symbol"] == t.normalizeSymbol(symbol) && p["side"] == "short" {
				if v, ok := p["positionAmt"].(float64); ok {
					quantity = math.Abs(v)
				}
			}
		}
		if quantity == 0 {
			return nil, fmt.Errorf("未找到 %s 的空仓", symbol)
		}
	}

	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}
	instId := t.normalizeSymbol(symbol)
	body := map[string]interface{}{
		"instId":     instId,
		"tdMode":     "cross",
		"side":       "buy",
		"ordType":    "market",
		"sz":         qstr,
		"posSide":    "short",
		"reduceOnly": true,
	}

	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return nil, fmt.Errorf("平空仓失败: %w", err)
	}
	var raw map[string]interface{}
	_ = json.Unmarshal(respBytes, &raw)
	log.Printf("✓ OKX 平空仓 %s 数量=%s", instId, qstr)
	return map[string]interface{}{"raw": raw}, nil
}

// SetLeverage 设置杠杆：OKX 的杠杆设置属于持仓模式/逐仓层面，OKX 支持在下单时指定开仓保证金方式或通过借贷等。
// 这里使用 /api/v5/account/set-leverage （若可用）或返回未实现提示。
// 注意：OKX API 对杠杆/逐仓/全仓的控制与其他交易所略有不同，建议在账户侧先做配置。:contentReference[oaicite:3]{index=3}
func (t *OkxTrader) SetLeverage(symbol string, leverage int) error {
	// OKX 的设置杠杆接口并非在所有账户类型相同，这里尝试调用 set-leverage（若平台支持）；
	// 若不支持，提示并返回 nil（不阻断交易流程）
	instId := t.normalizeSymbol(symbol)
	body := map[string]interface{}{
		"instId": instId,
		"lever":  strconv.Itoa(leverage),
	}
	ctx := context.Background()
	_, err := t.doRequest(ctx, "POST", "/api/v5/account/set-leverage", body, true, nil)
	if err != nil {
		// 不强制返回错误，记录告警
		log.Printf("⚠️ SetLeverage 调用失败: %v (可能该接口在你的账户类型不可用)", err)
		return nil
	}
	log.Printf("✓ %s 杠杆尝试设置为 %dx", instId, leverage)
	// 等待短暂冷却
	time.Sleep(3 * time.Second)
	return nil
}

// SetMarginMode 设置仓位模式 (true=全仓, false=逐仓)
// OKX 通过 tdMode 字段来控制： 'cross' 或 'isolated'
func (t *OkxTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	// OKX 的 margin 模式会在下单时通过 tdMode 指定，或可通过账户 API 修改。
	mode := "isolated"
	if isCrossMargin {
		mode = "cross"
	}
	// 在这里不强制调用账户变更 API（可能受限），仅记录并让调用方在下单时使用 tdMode。
	log.Printf("ℹ️ 设置 %s 为 %s 模式（调用方下单时请使用 tdMode=%s）", symbol, mode, mode)
	return nil
}

// GetMarketPrice 获取市场价格（本实现调用 /api/v5/market/ticker?instId=...）
func (t *OkxTrader) GetMarketPrice(symbol string) (float64, error) {
	instId := t.normalizeSymbol(symbol)
	query := map[string]string{"instId": instId}
	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/market/ticker", nil, false, query)
	if err != nil {
		return 0, fmt.Errorf("获取市场价失败: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return 0, err
	}
	// data[0].last
	if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
		if d0, ok := data[0].(map[string]interface{}); ok {
			if lastStr, ok := d0["last"].(string); ok {
				if fv, e := strconv.ParseFloat(lastStr, 64); e == nil {
					return fv, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("未能解析市场价")
}

// SetStopLoss 设置 止损 — 使用 OKX 条件订单 (ordType=conditional)
// OKX 条件/TP-SL 逻辑较复杂，API 提供 attachAlgoOrds 等高级字段。
// 这里我们使用一个最简单的条件单：ordType=conditional, triggerPx=stopPrice, ordType=market
// 注意：不同账户/模式下字段名和行为略有差异，实际使用请以 OKX 文档/返回为准。:contentReference[oaicite:4]{index=4}
func (t *OkxTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	instId := t.normalizeSymbol(symbol)
	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return err
	}

	side := "sell"
	posSide := "long"
	if strings.ToUpper(positionSide) != "LONG" {
		side = "buy"
		posSide = "short"
	}

	body := map[string]interface{}{
		"instId":    instId,
		"tdMode":    "cross",
		"side":      side,
		"ordType":   "conditional",
		"sz":        qstr,
		"posSide":   posSide,
		"triggerPx": fmt.Sprintf("%.8f", stopPrice), // 可能需要调整字段名（triggerPx/triggerPrice）
		// "ordPx": "-1", // -1 表示市价触发（视 API 支持）
		"reduceOnly": true,
	}

	ctx := context.Background()
	_, err = t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}
	log.Printf("✓ 已为 %s 设置止损（%s %s）", instId, qstr, fmt.Sprintf("%.4f", stopPrice))
	return nil
}

// SetTakeProfit 设置止盈（同样使用条件单）
// 注意：OKX 在部分接口上对 TP/SL 的附加参数有特定字段（attachAlgoOrds 等），复杂用例需按文档调整。:contentReference[oaicite:5]{index=5}
func (t *OkxTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	instId := t.normalizeSymbol(symbol)
	qstr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return err
	}

	side := "sell"
	posSide := "long"
	if strings.ToUpper(positionSide) != "LONG" {
		side = "buy"
		posSide = "short"
	}

	body := map[string]interface{}{
		"instId":     instId,
		"tdMode":     "cross",
		"side":       side,
		"ordType":    "conditional",
		"sz":         qstr,
		"posSide":    posSide,
		"triggerPx":  fmt.Sprintf("%.8f", takeProfitPrice),
		"reduceOnly": true,
		// 注：TP/SL 混合逻辑需参考 OKX 文档 attachAlgoOrds 字段以实现 OCO 等
	}

	ctx := context.Background()
	_, err = t.doRequest(ctx, "POST", "/api/v5/trade/order", body, true, nil)
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}
	log.Printf("✓ 已为 %s 设置止盈（%s %s）", instId, qstr, fmt.Sprintf("%.4f", takeProfitPrice))
	return nil
}

// CancelStopLossOrders 取消止损单（通过拉取挂单并基于订单类型过滤）
// OKX 的止损/止盈通常是 ordType = "conditional"
func (t *OkxTrader) CancelStopLossOrders(symbol string) error {
	instId := t.normalizeSymbol(symbol)
	// 先拉取挂单
	query := map[string]string{"instId": instId}
	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/trade/orders-pending", nil, true, query)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return err
	}
	count := 0
	if data, ok := raw["data"].([]interface{}); ok {
		for _, di := range data {
			if d, ok := di.(map[string]interface{}); ok {
				ordType := ""
				if v, ok := d["ordType"].(string); ok {
					ordType = v
				}
				ordId := ""
				if v, ok := d["ordId"].(string); ok {
					ordId = v
				}
				// 只取消条件单（可能为止损）
				if ordType == "conditional" && ordId != "" {
					// 调用取消单接口
					cbody := map[string]interface{}{
						"instId": instId,
						"ordId":  ordId,
					}
					_, cerr := t.doRequest(ctx, "POST", "/api/v5/trade/cancel-order", cbody, true, nil)
					if cerr != nil {
						log.Printf("  ⚠ 取消订单 %s 失败: %v", ordId, cerr)
						continue
					}
					count++
					log.Printf("  ✓ 已取消条件单 %s", ordId)
				}
			}
		}
	}
	if count == 0 {
		log.Printf("ℹ %s 没有条件单需要取消", instId)
	}
	return nil
}

// CancelTakeProfitOrders 取消止盈单（同 CancelStopLossOrders）
func (t *OkxTrader) CancelTakeProfitOrders(symbol string) error {
	return t.CancelStopLossOrders(symbol)
}

// CancelAllOrders 取消该币种全部挂单：读取挂单后逐个 cancel
func (t *OkxTrader) CancelAllOrders(symbol string) error {
	instId := t.normalizeSymbol(symbol)
	query := map[string]string{"instId": instId}
	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/trade/orders-pending", nil, true, query)
	if err != nil {
		return fmt.Errorf("获取挂单失败: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return err
	}
	cancelCnt := 0
	if data, ok := raw["data"].([]interface{}); ok {
		for _, di := range data {
			if d, ok := di.(map[string]interface{}); ok {
				ordId := ""
				if v, ok := d["ordId"].(string); ok {
					ordId = v
				}
				if ordId == "" {
					continue
				}
				cbody := map[string]interface{}{
					"instId": instId,
					"ordId":  ordId,
				}
				_, cerr := t.doRequest(ctx, "POST", "/api/v5/trade/cancel-order", cbody, true, nil)
				if cerr != nil {
					log.Printf("  ⚠ 取消订单 %s 失败: %v", ordId, cerr)
					continue
				}
				cancelCnt++
			}
		}
	}
	log.Printf("✓ 已取消 %s 的 %d 个挂单", instId, cancelCnt)
	return nil
}

// CancelStopOrders 取消止盈/止损单（调用 CancelAllOrders 的条件单过滤版本）
func (t *OkxTrader) CancelStopOrders(symbol string) error {
	return t.CancelAllOrders(symbol)
}

// FormatQuantity 格式化数量到正确精度（查询 public instruments）
func (t *OkxTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	instId := t.normalizeSymbol(symbol)
	// 查询 instruments 信息
	query := map[string]string{"instType": "SWAP", "instId": instId}
	ctx := context.Background()
	respBytes, err := t.doRequest(ctx, "GET", "/api/v5/public/instruments", nil, false, query)
	if err != nil {
		// 如果查询失败，回退到简单的 3 位小数
		return fmt.Sprintf("%.3f", quantity), nil
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err == nil {
		if data, ok := raw["data"].([]interface{}); ok && len(data) > 0 {
			if d0, ok := data[0].(map[string]interface{}); ok {
				// 常见字段："lotSz" 或 "minSz" 或 "sz" 等，取其中一个来计算精度
				var step string
				if v, ok := d0["lotSz"].(string); ok && v != "" {
					step = v
				} else if v, ok := d0["minSz"].(string); ok && v != "" {
					step = v
				} else if v, ok := d0["sz"].(string); ok && v != "" {
					step = v
				}
				if step != "" {
					prec := calculatePrecision(step)
					format := fmt.Sprintf("%%.%df", prec)
					return fmt.Sprintf(format, quantity), nil
				}
			}
		}
	}
	// 兜底
	return fmt.Sprintf("%.3f", quantity), nil
}
