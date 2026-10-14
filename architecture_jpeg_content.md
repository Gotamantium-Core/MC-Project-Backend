# Microcontrollers Project Architecture

## System Overview
* **Fast Lane (Meals only):** Features an entry display unit showing count data (e.g., `91`), a sensor/reader node, and a server connection line.
* **Slow Lane (Etc. / Drinks / Snacks):** Features an entry display unit showing count data (e.g., `27`), a sensor/reader node, and a connection routing towards a Wi-Fi-enabled Cash Counter.
* **Order Item Concept:** Includes items like a `BIRIYANI x1` item display card with status indicators.

## Architecture Components
* **Server:** Central server handling requests from client interfaces.
* **Database (DB):** Connected to the server for storing application state, user accounts, menus, and orders.
* **Broker:** Wireless messaging broker node communicating with the server and peripherals.
* **Mobile / Terminal Client:** User interface displaying dashboard cards, items, and navigation elements.

## Database Schema
* `users (id, student_roll, name, email, phone)`
* `menu (id, item_name, code, available, price)`
* `orders (user_id, items, timestamp)`
* `order_items (order_id, user_item_id, qty)`
* `user_ledger (user_id, order_id, credit, amount)`