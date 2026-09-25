## Database Schema
```
users
  ├── id               [PK]
  ├── roll_no          [UNIQUE] (TVE24CSXXX format)
  ├── name
  ├── phone
  └── created_at

menu
  ├── id               [PK]
  ├── item_name
  ├── code             [UNIQUE]
  ├── price
  ├── is_available
  └── created_at

orders
  ├── id               [PK]
  ├── user_id          [FK -> users.id]
  ├── status
  ├── subtotal
  ├── total
  ├── created_at
  └── completed_at

order_items
  ├── id               [PK]
  ├── order_id         [FK -> orders.id]
  ├── menu_id          [FK -> menu.id]
  ├── item_name        -- snapshot of the name
  ├── unit_price       -- snapshot of the price
  ├── quantity
  └── line_total

transactions
  ├── id               [PK]
  ├── user_id          [FK -> users.id]
  ├── order_id         [FK -> orders.id] (nullable)
  ├── amount
  ├── transaction_type
  └── created_at
```
  ## Functions Added (currently)
(All files are in the `database` directory)

### General functions (db.go)
- `Open` - opens the database
- `Initialize` - initializes the database using schema.sql

### Users (users.go)
- `CreateUser` - creates a user and adds it to the table
- `GetUserByID` - fetches user's row by ID. 
- `GetUserByRollNo` - fetches user's row by unique RollNo (TVE24CSXXX format)
- `UpdateUser` - allows you to update a user's name, phone, or roll number based on their id (taken from the user data structure). 
- `ListUsers` - returns all rows in the users table
