package dkapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mcavage/dk-cli/internal/money"
)

// Order history, via DigiKey's Order Status v4 API.
//
// This exists because the alternative is reconstructing what you own by
// searching Gmail for receipts and regex-parsing an HTML table out of a base64
// body. Order history is the ground truth for "what do I already have", which
// is the input to every "what do I still need to buy" question.
//
// Two things about DigiKey's model that will bite anyone who assumes otherwise:
//
// An Order contains one or more SalesOrders. DigiKey's own docs say the number
// printed on the website, the packing slip and the invoice is the SALES ORDER
// ID, not the OrderNumber. So the number a human reads off a box is the one
// RetrieveSalesOrder wants, and looking it up as an OrderNumber fails.
//
// Under 2-legged OAuth the X-DIGIKEY-Account-Id header is REQUIRED, not
// optional as it is for 3-legged. Without it the call fails in a way that looks
// like an auth problem rather than a missing header.

const (
	orderStatusBase = "/orderstatus/v4"

	// maxOrderPageSize is DigiKey's documented ceiling. Asking for more is
	// silently capped, which would make a caller believe it had every order.
	maxOrderPageSize = 25
)

// ErrNoAccountID means 2-legged order history was attempted without the account
// id that DigiKey requires for it.
var ErrNoAccountID = errors.New("dkapi: DigiKey account id required for order history")

// OrderSummary is one Order as the history view presents it.
type OrderSummary struct {
	OrderNumber  string       `json:"order_number"`
	DateEntered  string       `json:"date"`
	Currency     string       `json:"currency,omitempty"`
	PONumber     string       `json:"po_number,omitempty"`
	Status       string       `json:"status,omitempty"`
	SalesOrders  []SalesOrder `json:"sales_orders,omitempty"`
	TotalPrice   money.Micro  `json:"-"`
	TotalDisplay string       `json:"total"`
}

// SalesOrder is the unit a human actually has a number for.
type SalesOrder struct {
	// SalesOrderID is the number printed on the packing slip and invoice.
	SalesOrderID string      `json:"sales_order_id"`
	OrderNumber  string      `json:"order_number,omitempty"`
	Status       string      `json:"status,omitempty"`
	DateEntered  string      `json:"date,omitempty"`
	ShipMethod   string      `json:"ship_method,omitempty"`
	Currency     string      `json:"currency,omitempty"`
	TotalPrice   money.Micro `json:"-"`
	TotalDisplay string      `json:"total"`
	Items        []OrderItem `json:"items,omitempty"`
}

// OrderItem is one purchased line. These fields are chosen to answer "what do I
// own and how much of it", which is the whole point of pulling history.
type OrderItem struct {
	DKPartNumber string `json:"dk_pn"`
	MPN          string `json:"mpn,omitempty"`
	Description  string `json:"description,omitempty"`
	PackType     string `json:"pack_type,omitempty"`
	CustomerRef  string `json:"customer_reference,omitempty"`

	QtyOrdered   int `json:"qty_ordered"`
	QtyShipped   int `json:"qty_shipped"`
	QtyBackorder int `json:"qty_backorder"`

	UnitPrice    money.Micro `json:"-"`
	UnitDisplay  string      `json:"unit_price,omitempty"`
	TotalPrice   money.Micro `json:"-"`
	TotalDisplay string      `json:"line_total,omitempty"`

	Shipments []Shipment `json:"shipments,omitempty"`
}

// Outstanding reports units paid for but not yet received, which is the number
// that decides whether a part is really on hand.
func (i OrderItem) Outstanding() int {
	n := i.QtyOrdered - i.QtyShipped
	if n < 0 {
		return 0
	}
	return n
}

// Shipment is a tracking record.
type Shipment struct {
	QtyShipped     int    `json:"qty_shipped"`
	ShippedDate    string `json:"shipped_date,omitempty"`
	TrackingNumber string `json:"tracking_number,omitempty"`
	ExpectedDate   string `json:"expected_delivery,omitempty"`
	InvoiceID      string `json:"invoice_id,omitempty"`
}

// OrderHistory is a page of results.
type OrderHistory struct {
	Orders      []OrderSummary `json:"orders"`
	TotalOrders int            `json:"total_orders"`
	Page        int            `json:"page"`
	PageSize    int            `json:"page_size"`
	HasMore     bool           `json:"has_more"`
}

// OrderSearchOptions narrows a history query.
type OrderSearchOptions struct {
	Start    time.Time
	End      time.Time
	Page     int
	PageSize int

	// Shared returns every order on the account rather than only the
	// authenticated user's.
	Shared bool
}

// SearchOrders lists orders in a date range.
func (c *Client) SearchOrders(ctx context.Context, opts OrderSearchOptions) (*OrderHistory, []byte, error) {
	if c.accountID == "" {
		return nil, nil, ErrNoAccountID
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	switch {
	case opts.PageSize <= 0:
		opts.PageSize = maxOrderPageSize
	case opts.PageSize > maxOrderPageSize:
		// Silently accepting a larger value would let a caller believe it had
		// every order when DigiKey capped the page behind its back.
		return nil, nil, fmt.Errorf("dkapi: page size %d exceeds DigiKey's maximum of %d",
			opts.PageSize, maxOrderPageSize)
	}

	q := url.Values{}
	q.Set("PageNumber", strconv.Itoa(opts.Page))
	q.Set("PageSize", strconv.Itoa(opts.PageSize))
	if opts.Shared {
		q.Set("Shared", "true")
	}
	if !opts.Start.IsZero() {
		q.Set("StartDate", opts.Start.Format("2006-01-02"))
	}
	if !opts.End.IsZero() {
		q.Set("EndDate", opts.End.Format("2006-01-02"))
	}

	// GET /orderstatus/v4/orders. The developer portal renders this operation
	// under an "OrderHistory / SearchOrders" heading, which are the group and
	// operation names, NOT path segments: using them returns a 404 with
	// "Invalid resource path".
	path := orderStatusBase + "/orders?" + q.Encode()
	// The account id is REQUIRED here under 2-legged auth: without it DigiKey
	// cannot tell whose sales orders to return.
	raw, err := c.doWithHeaders(ctx, http.MethodGet, path, nil,
		map[string]string{"X-DIGIKEY-Account-Id": c.accountID})
	if err != nil {
		return nil, nil, err
	}

	var wire wireOrderResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, raw, fmt.Errorf("dkapi: decoding order history: %w", err)
	}

	h := &OrderHistory{
		TotalOrders: wire.TotalOrders,
		Page:        opts.Page,
		PageSize:    opts.PageSize,
	}
	for _, o := range wire.Orders {
		h.Orders = append(h.Orders, toOrderSummary(o))
	}
	h.HasMore = opts.Page*opts.PageSize < wire.TotalOrders
	return h, raw, nil
}

// RetrieveSalesOrder fetches one sales order by the id printed on the packing
// slip and invoice.
func (c *Client) RetrieveSalesOrder(ctx context.Context, salesOrderID string) (*SalesOrder, []byte, error) {
	id := strings.TrimSpace(salesOrderID)
	if id == "" {
		return nil, nil, errors.New("dkapi: empty sales order id")
	}
	// GET /orderstatus/v4/salesorder/{id}. This endpoint must NOT receive the
	// account-id header: DigiKey's changelog says to remove it, and it resolves
	// the order against every account linked to the authenticated user instead.
	path := orderStatusBase + "/salesorder/" + url.PathEscape(id)
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	var wire wireSalesOrder
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, raw, fmt.Errorf("dkapi: decoding sales order: %w", err)
	}
	if numString(wire.SalesOrderID) == "" && len(wire.LineItems) == 0 {
		return nil, raw, ErrNotFound
	}
	so := toSalesOrder(wire)
	return &so, raw, nil
}

// wire types

type wireOrderResponse struct {
	TotalOrders int         `json:"TotalOrders"`
	Orders      []wireOrder `json:"Orders"`
}

type wireOrder struct {
	OrderNumber       json.Number      `json:"OrderNumber"`
	CustomerID        json.Number      `json:"CustomerId"`
	DateEntered       string           `json:"DateEntered"`
	Currency          string           `json:"Currency"`
	PONumber          string           `json:"PONumber"`
	EntireOrderStatus wireStatus       `json:"EntireOrderStatus"`
	SalesOrders       []wireSalesOrder `json:"SalesOrders"`
}

type wireStatus struct {
	OrderStatus      string `json:"OrderStatus"`
	SalesOrderStatus string `json:"SalesOrderStatus"`
	ShortDescription string `json:"ShortDescription"`
}

func (s wireStatus) text() string {
	for _, v := range []string{s.SalesOrderStatus, s.OrderStatus, s.ShortDescription} {
		if v != "" && v != "Unknown" {
			return v
		}
	}
	if s.ShortDescription != "" {
		return s.ShortDescription
	}
	return "Unknown"
}

type wireSalesOrder struct {
	SalesOrderID json.Number    `json:"SalesOrderId"`
	OrderNumber  json.Number    `json:"OrderNumber"`
	Status       wireStatus     `json:"Status"`
	TotalPrice   float64        `json:"TotalPrice"`
	DateEntered  string         `json:"DateEntered"`
	ShipMethod   string         `json:"ShipMethod"`
	Currency     string         `json:"Currency"`
	LineItems    []wireLineItem `json:"LineItems"`
}

type wireLineItem struct {
	DigiKeyProductNumber      string             `json:"DigiKeyProductNumber"`
	ManufacturerProductNumber string             `json:"ManufacturerProductNumber"`
	Description               string             `json:"Description"`
	PackType                  string             `json:"PackType"`
	CustomerReference         string             `json:"CustomerReference"`
	QuantityOrdered           int                `json:"QuantityOrdered"`
	QuantityShipped           int                `json:"QuantityShipped"`
	QuantityBackOrder         int                `json:"QuantityBackOrder"`
	UnitPrice                 float64            `json:"UnitPrice"`
	TotalPrice                float64            `json:"TotalPrice"`
	ItemShipments             []wireItemShipment `json:"ItemShipments"`
}

type wireItemShipment struct {
	QuantityShipped      int         `json:"QuantityShipped"`
	InvoiceID            json.Number `json:"InvoiceId"`
	ShippedDate          string      `json:"ShippedDate"`
	TrackingNumber       string      `json:"TrackingNumber"`
	ExpectedDeliveryDate string      `json:"ExpectedDeliveryDate"`
}

func toOrderSummary(o wireOrder) OrderSummary {
	s := OrderSummary{
		OrderNumber: numString(o.OrderNumber),
		DateEntered: trimDate(o.DateEntered),
		Currency:    o.Currency,
		PONumber:    o.PONumber,
		Status:      o.EntireOrderStatus.text(),
	}
	for _, so := range o.SalesOrders {
		conv := toSalesOrder(so)
		s.TotalPrice += conv.TotalPrice
		s.SalesOrders = append(s.SalesOrders, conv)
	}
	s.TotalDisplay = s.TotalPrice.String()
	return s
}

func toSalesOrder(so wireSalesOrder) SalesOrder {
	out := SalesOrder{
		SalesOrderID: numString(so.SalesOrderID),
		OrderNumber:  numString(so.OrderNumber),
		Status:       so.Status.text(),
		DateEntered:  trimDate(so.DateEntered),
		ShipMethod:   so.ShipMethod,
		Currency:     so.Currency,
		TotalPrice:   money.FromFloat(so.TotalPrice),
	}
	out.TotalDisplay = out.TotalPrice.String()
	for _, li := range so.LineItems {
		item := OrderItem{
			DKPartNumber: li.DigiKeyProductNumber,
			MPN:          li.ManufacturerProductNumber,
			Description:  li.Description,
			PackType:     li.PackType,
			CustomerRef:  li.CustomerReference,
			QtyOrdered:   li.QuantityOrdered,
			QtyShipped:   li.QuantityShipped,
			QtyBackorder: li.QuantityBackOrder,
			UnitPrice:    money.FromFloat(li.UnitPrice),
			TotalPrice:   money.FromFloat(li.TotalPrice),
		}
		item.UnitDisplay = item.UnitPrice.Exact()
		item.TotalDisplay = item.TotalPrice.String()
		for _, sh := range li.ItemShipments {
			item.Shipments = append(item.Shipments, Shipment{
				QtyShipped:     sh.QuantityShipped,
				ShippedDate:    trimDate(sh.ShippedDate),
				TrackingNumber: sh.TrackingNumber,
				ExpectedDate:   trimDate(sh.ExpectedDeliveryDate),
				InvoiceID:      numString(sh.InvoiceID),
			})
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// numString renders a JSON number without scientific notation or a spurious
// decimal. DigiKey sends order and invoice ids as bare integers large enough
// that decoding them into float64 would corrupt the last digits.
func numString(n json.Number) string {
	s := strings.TrimSpace(n.String())
	if s == "" || s == "0" {
		return ""
	}
	return s
}
