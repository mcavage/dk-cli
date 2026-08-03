package main

import (
	"strings"

	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/money"
)

func errNoAccountID() error { return dkapi.ErrNoAccountID }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func ordersFixture() []dkapi.OrderSummary {
	unit, _ := money.ParseMicro("0.578")
	return []dkapi.OrderSummary{{
		OrderNumber: "123456789012345",
		DateEntered: "2026-05-30",
		SalesOrders: []dkapi.SalesOrder{{
			SalesOrderID: "88123456",
			DateEntered:  "2026-05-30",
			Items: []dkapi.OrderItem{
				{
					DKPartNumber: "P5555-ND", MPN: "ECA-1VHG102",
					Description: "CAP ALUM 1000UF 20% 35V RADIAL",
					QtyOrdered:  20, QtyShipped: 14, QtyBackorder: 3,
					UnitPrice: unit, UnitDisplay: unit.Exact(),
				},
				{
					DKPartNumber: "311-10.0KCRCT-ND", MPN: "RC0805FR-0710KL",
					QtyOrdered: 10, QtyShipped: 10,
				},
			},
		}},
	}}
}
