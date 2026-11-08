---
slug: slippage
title: "Slippage: Why Orders Fill Off-Price"
summary: "How execution latency and volume affect order fill prices in live markets."
tags: [execution, risk, trading]
---

## What is Slippage?

Slippage is the difference between the expected price of a trade and the price at which the trade is executed. In fast-moving markets or when placing large orders, the price you see on your screen when you click submit is rarely guaranteed to be the exact price of execution. In TradeEdu, orders above 100 shares incur simulated volume slippage of 0.50% to reflect this realistic market friction. Understanding slippage helps beginners avoid costly surprises when entering and exiting positions.

## Causes of Execution Slippage

Two primary factors cause slippage: market volatility and execution latency. Volatility means that between the moment an order is dispatched and the moment an exchange matching engine processes it, prices have already shifted. Latency refers to network transit time and broker queueing delays. High volume orders can also exhaust the immediate liquidity at the top of the order book, forcing remaining shares to fill at progressively worse price levels deeper in the book.

## Managing and Minimizing Slippage

Traders manage slippage primarily by using limit orders rather than market orders. A limit order establishes a strict ceiling on purchase price or a floor on selling price, guaranteeing price protection at the expense of fill certainty. Furthermore, avoiding market orders during periods of extreme illiquidity or around major earnings announcements significantly reduces the magnitude of unexpected price drift.
