package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/output"
)

// Order history. This exists because the alternative is reconstructing what you
// own by searching your email for receipts and parsing HTML out of them.
//
// The default output is deliberately flattened per line item rather than nested
// per order, because the question being asked is almost never "show me order
// 12345", it is "what did I buy and how much of it do I still have".

func ordersList(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	client, cerr := newPartClient()
	if cerr != nil {
		return output.Failure("orders.list", cerr), ""
	}

	start, end, derr := resolveDateRange(fv.Str("since"), fv.Str("start"), fv.Str("end"))
	if derr != nil {
		return output.Failure("orders.list", output.NewError(output.BadArg, derr.Error(), false,
			"dk orders list --since 6m   # or --start 2026-01-01 --end 2026-06-30")), ""
	}

	opts := dkapi.OrderSearchOptions{
		Start:  start,
		End:    end,
		Page:   fv.Int("page"),
		Shared: fv.Bool("shared"),
	}

	all := fv.Bool("all")
	var orders []dkapi.OrderSummary
	var total, pages int
	truncated := false

	for {
		h, _, err := client.SearchOrders(context.Background(), opts)
		if err != nil {
			return output.Failure("orders.list", classifyOrderErr(err)), ""
		}
		orders = append(orders, h.Orders...)
		total = h.TotalOrders
		pages++

		if !all || !h.HasMore {
			truncated = h.HasMore
			break
		}
		// A hard page ceiling. DigiKey caps a page at 25, so an unbounded loop
		// over a long history could quietly spend a large share of the 1000/day
		// quota; stopping and saying so beats an invisible bill.
		if pages >= 20 {
			truncated = true
			break
		}
		opts.Page++
	}

	data := map[string]any{
		"orders":        orders,
		"total_orders":  total,
		"pages_fetched": pages,
		"start":         dateStr(start),
		"end":           dateStr(end),
	}
	if fv.Bool("items") {
		data["items"] = flattenItems(orders)
	}

	env := output.Success("orders.list", data)
	if truncated {
		// Never let a caller believe a partial history is the whole history.
		env.AddWarning(output.Warning{Code: output.ResultTruncated,
			Message: fmt.Sprintf("fetched %d of %d orders; re-run with --all, or narrow the range",
				len(orders), total)})
	}
	env.WithMeta(&output.Meta{RateLimit: rateLimitMeta(client.LastRateLimit)})
	return env, ""
}

func orderGet(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	id := args[0]

	client, cerr := newPartClient()
	if cerr != nil {
		return output.Failure("order.get", cerr), ""
	}

	so, _, err := client.RetrieveSalesOrder(context.Background(), id)
	if err != nil {
		e := classifyOrderErr(err)
		// The single most likely user error here, straight from DigiKey's own
		// docs: the number on the website, packing slip and invoice is the
		// SalesOrderId, not the OrderNumber. Looking one up as the other fails
		// with a bare not-found unless we say this.
		if e.Code == output.NoMatch {
			e = output.NewError(output.NoMatch,
				fmt.Sprintf("no sales order %s", id), false,
				"dk orders list --since 12m   # find the sales order id").
				WithDetails(map[string]any{
					"note": "DigiKey prints the SALES ORDER ID on the website, packing slip " +
						"and invoice, not the order number. An order contains one or more " +
						"sales orders, and this command wants the sales order id.",
				})
		}
		return output.Failure("order.get", e), ""
	}

	env := output.Success("order.get", so)
	env.WithMeta(&output.Meta{RateLimit: rateLimitMeta(client.LastRateLimit)})
	return env, ""
}

// flatItem is one purchased line, denormalized across orders, which is the
// shape needed to answer "how many of these do I have".
type flatItem struct {
	SalesOrderID string `json:"sales_order_id"`
	Date         string `json:"date"`
	DKPartNumber string `json:"dk_pn"`
	MPN          string `json:"mpn,omitempty"`
	Description  string `json:"description,omitempty"`
	CustomerRef  string `json:"customer_reference,omitempty"`
	QtyOrdered   int    `json:"qty_ordered"`
	QtyShipped   int    `json:"qty_shipped"`
	Outstanding  int    `json:"outstanding"`
	UnitPrice    string `json:"unit_price,omitempty"`
	LineTotal    string `json:"line_total,omitempty"`
}

func flattenItems(orders []dkapi.OrderSummary) []flatItem {
	out := []flatItem{}
	for _, o := range orders {
		for _, so := range o.SalesOrders {
			for _, it := range so.Items {
				out = append(out, flatItem{
					SalesOrderID: so.SalesOrderID,
					Date:         firstNonEmptyStr(so.DateEntered, o.DateEntered),
					DKPartNumber: it.DKPartNumber,
					MPN:          it.MPN,
					Description:  it.Description,
					CustomerRef:  it.CustomerRef,
					QtyOrdered:   it.QtyOrdered,
					QtyShipped:   it.QtyShipped,
					Outstanding:  it.Outstanding(),
					UnitPrice:    it.UnitDisplay,
					LineTotal:    it.TotalDisplay,
				})
			}
		}
	}
	return out
}

var sinceRE = regexp.MustCompile(`^(\d+)\s*([dwmy])$`)

// resolveDateRange turns --since 6m, or an explicit --start/--end pair, into a
// concrete range.
//
// --since is relative and --start/--end are absolute; combining them is a
// contradiction, so it is an error rather than a silent precedence rule the
// caller has to memorize.
func resolveDateRange(since, startStr, endStr string) (time.Time, time.Time, error) {
	since = strings.TrimSpace(since)
	startStr = strings.TrimSpace(startStr)
	endStr = strings.TrimSpace(endStr)

	if since != "" && (startStr != "" || endStr != "") {
		return time.Time{}, time.Time{}, fmt.Errorf("--since cannot be combined with --start or --end")
	}

	now := time.Now()
	if since != "" {
		m := sinceRE.FindStringSubmatch(strings.ToLower(since))
		if m == nil {
			return time.Time{}, time.Time{}, fmt.Errorf(
				"--since %q: want a number and a unit, e.g. 30d, 6w, 6m, 2y", since)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return time.Time{}, time.Time{}, fmt.Errorf("--since %q: needs a positive number", since)
		}
		var start time.Time
		switch m[2] {
		case "d":
			start = now.AddDate(0, 0, -n)
		case "w":
			start = now.AddDate(0, 0, -7*n)
		case "m":
			start = now.AddDate(0, -n, 0)
		case "y":
			start = now.AddDate(-n, 0, 0)
		}
		return start, now, nil
	}

	var start, end time.Time
	var err error
	if startStr != "" {
		if start, err = time.Parse("2006-01-02", startStr); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--start %q: want YYYY-MM-DD", startStr)
		}
	}
	if endStr != "" {
		if end, err = time.Parse("2006-01-02", endStr); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--end %q: want YYYY-MM-DD", endStr)
		}
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("--end %s is before --start %s", endStr, startStr)
	}
	// Default matches DigiKey's own: the last 30 days.
	if start.IsZero() && end.IsZero() {
		return now.AddDate(0, 0, -30), now, nil
	}
	return start, end, nil
}

// classifyOrderErr maps an order-history failure onto the envelope, keeping the
// two setup mistakes distinguishable from a real auth failure.
func classifyOrderErr(err error) *output.Error {
	if err == dkapi.ErrNoAccountID {
		return output.NewError(output.NoCredentials,
			"order history needs your DigiKey account id: under 2-legged OAuth DigiKey "+
				"cannot tell whose sales orders to return without it", false,
			"export DK_ACCOUNT_ID=<your DigiKey account id>   # find it under My Account on digikey.com")
	}
	e := classifyDKErr(err)
	if e.Code == output.BadCredentials || e.Code == output.NotSubscribed {
		e = e.WithDetails(map[string]any{
			"note": "order history is a separate API product. Subscribe your app to " +
				"Order Status at developer.digikey.com; a Product Information V4 " +
				"subscription alone returns 401 here.",
		})
	}
	return e
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func dateStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
