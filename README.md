﻿# Банка 🏺

**Банка** — это легковесная очередь сообщений, написанная на Go, которая предоставляет надежный и быстрый способ обработки сообщений. Этот проект вдохновлен такими решениями, как RabbitMQ и Kafka, но стремится быть максимально простым в использовании и настройке.

## 🚀 Особенности

📦 **Легковесность:** Минимальные зависимости и простая установка.

⚡ **Высокая производительность:** Оптимизирован для быстрого обмена сообщениями.

🔧 **Настраиваемость:** Гибкость в настройке для различных сценариев использования.

🔒 **Надежность:** Гарантия доставки сообщений при правильной настройке.

🛠 **Простота:** Удобный интерфейс для разработчиков.

## 🛠 Установка

1. Убедитесь, что у вас установлен Go (версия 1.20+).
2. Клонируйте репозиторий:
    ```bash
    git clone https://github.com/d4got10/banka.git
    cd banka
    ```
3. Соберите проект:
    ```bash
    go build
    ```

## ⚙️ Как использовать

Пример

```go
package main

import (
    "fmt"
    "github.com/d4got10/banka"
)

func main() {
    queue := banka.NewQueue()

    // Отправка сообщения
    queue.Publish("Привет, мир!")

    // Получение сообщения
    msg, err := queue.Consume()
    if err != nil {
        fmt.Println("Ошибка:", err)
        return
    }
    fmt.Println("Получено сообщение:", msg)
}
```

## 📚 Документация

Полная документация доступна в [Wiki](https://github.com/d4got10/banka/wiki).

## 🤝 Вклад в проект

Если у вас есть идеи или вы хотите внести изменения:

1. Сделайте форк репозитория.
2. Создайте новую ветку: `git checkout -b feature/имя-фичи`.
3. Внесите изменения и сделайте коммит: `git commit -m "Добавлена новая функция"`.
4. Отправьте изменения: `git push origin feature/имя-фичи`.
5. Создайте Pull Request.

## 🛡 Лицензия

Этот проект распространяется под лицензией MIT. Подробнее см. в файле LICENSE.

## 💡 Контакты

Если у вас есть вопросы или предложения, пишите в телеграм @d4got10.