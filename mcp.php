<?php
/**
 * Model Context Protocol (MCP) Server in PHP
 * Supports MCP SSE (Server-Sent Events) Transport + HTTP JSON-RPC 2.0
 */

require_once __DIR__ . '/config.php';
require_once __DIR__ . '/lib.php';

// Cross-Origin Resource Sharing (CORS) headers for remote MCP clients
header('Access-Control-Allow-Origin: *');
header('Access-Control-Allow-Methods: GET, POST, OPTIONS');
header('Access-Control-Allow-Headers: Content-Type, Authorization, X-Session-Id');

if ($_SERVER['REQUEST_METHOD'] === 'OPTIONS') {
    http_response_code(204);
    exit;
}

$action = $_GET['action'] ?? ($_SERVER['REQUEST_METHOD'] === 'GET' ? 'sse' : 'message');
$sessionId = $_GET['sessionId'] ?? ($_GET['session_id'] ?? ($_SERVER['HTTP_X_SESSION_ID'] ?? null));

// Helper: Session queue file path
function get_session_queue_file($sid) {
    $clean_sid = preg_replace('/[^a-zA-Z0-9_\-]/', '', $sid);
    return sys_get_temp_dir() . '/mcp_session_' . $clean_sid . '.json';
}

// -------------------------------------------------------------------------
// 1. SSE Stream Endpoint (GET /mcp.php?action=sse or GET /mcp.php)
// -------------------------------------------------------------------------
if ($action === 'sse') {
    // Disable all output buffering for real-time SSE streaming
    set_time_limit(0);
    ini_set('max_execution_time', '0');
    ini_set('output_buffering', 'off');
    ini_set('zlib.output_compression', 'off');

    while (ob_get_level()) {
        ob_end_clean();
    }
    ob_implicit_flush(true);

    header('Content-Type: text/event-stream');
    header('Cache-Control: no-cache');
    header('Connection: keep-alive');
    header('X-Accel-Buffering: no'); // Disable buffering in Nginx / Caddy

    // Generate unique session ID
    $sid = bin2hex(random_bytes(16));
    $queueFile = get_session_queue_file($sid);
    file_put_contents($queueFile, json_encode([]));

    // Base URI resolution
    $scheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $host = $_SERVER['HTTP_HOST'] ?? 'localhost';
    $script = $_SERVER['SCRIPT_NAME'];
    $messageUrl = "{$scheme}://{$host}{$script}?action=message&sessionId={$sid}";

    // Emit initial 'endpoint' SSE event per MCP specification
    echo "event: endpoint\n";
    echo "data: {$messageUrl}\n\n";
    flush();

    $lastPing = time();

    // Event polling loop
    while (true) {
        if (connection_aborted()) {
            if (file_exists($queueFile)) {
                @unlink($queueFile);
            }
            break;
        }

        // Check for queued messages to stream
        if (file_exists($queueFile)) {
            $raw = file_get_contents($queueFile);
            $queue = json_decode($raw, true) ?: [];
            if (!empty($queue)) {
                file_put_contents($queueFile, json_encode([])); // clear queue
                foreach ($queue as $msg) {
                    echo "event: message\n";
                    echo "data: " . json_encode($msg, JSON_UNESCAPED_UNICODE) . "\n\n";
                    flush();
                }
            }
        }

        // Send periodic SSE keep-alive comment
        if (time() - $lastPing >= 15) {
            echo ": keepalive\n\n";
            flush();
            $lastPing = time();
        }

        usleep(200000); // 200ms sleep
    }
    exit;
}

// -------------------------------------------------------------------------
// 2. MCP Tools Definition Registry
// -------------------------------------------------------------------------
function get_mcp_tools() {
    return [
        [
            'name' => 'search_tiles',
            'description' => 'Semantic vector search across profile cards by query or returns ranking. Returns tile metadata, teasers, and similarity scores.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'q' => ['type' => 'string', 'description' => 'Natural language query or keywords (e.g., "IT consulting", "Krypto", "Parkour")'],
                    'lang' => ['type' => 'string', 'enum' => ['de', 'en'], 'description' => 'Preferred language (default "de")'],
                    'limit' => ['type' => 'integer', 'description' => 'Maximum tiles to return (default 20)'],
                    'offset' => ['type' => 'integer', 'description' => 'Pagination offset (default 0)']
                ]
            ]
        ],
        [
            'name' => 'get_similar_tiles',
            'description' => 'Find tiles semantically similar to an existing card by name.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the source tile (e.g. "hero", "finance", "it")'],
                    'lang' => ['type' => 'string', 'enum' => ['de', 'en'], 'description' => 'Preferred language (default "de")'],
                    'limit' => ['type' => 'integer', 'description' => 'Maximum tiles to return (default 5)'],
                    'offset' => ['type' => 'integer', 'description' => 'Pagination offset (default 0)']
                ],
                'required' => ['name']
            ]
        ],
        [
            'name' => 'get_tile',
            'description' => 'Fetch full metadata and optional detailed article content for a specific card by name and language.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the card (e.g. "hero", "finance", "it", "contact")'],
                    'lang' => ['type' => 'string', 'enum' => ['de', 'en'], 'description' => 'Language code (default "de")'],
                    'include_content' => ['type' => 'boolean', 'description' => 'Whether to include the full HTML article content from file (default true)']
                ],
                'required' => ['name']
            ]
        ],
        [
            'name' => 'create_tile',
            'description' => 'Create a new card with title, summary, html_teaser, and auto-generated vector embedding via Ollama.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Unique slug identifier (e.g., "ai-consulting")'],
                    'title' => ['type' => 'string', 'description' => 'Display title of the tile'],
                    'language' => ['type' => 'string', 'enum' => ['de', 'en'], 'description' => 'Language code (default "de")'],
                    'summary' => ['type' => 'string', 'description' => 'Structured summary for vector search indexing'],
                    'html_teaser' => ['type' => 'string', 'description' => 'Grid tile preview HTML snippet'],
                    'tags' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Category tags'],
                    'type' => ['type' => 'string', 'enum' => ['doc', 'link'], 'description' => 'Card behavior type'],
                    'link' => ['type' => 'string', 'description' => 'Target URL if type is "link"'],
                    'content_file' => ['type' => 'string', 'description' => 'HTML content partial filename (e.g., "ai_de.html")'],
                    'accent_color' => ['type' => 'string', 'description' => 'Hex accent color (e.g. "#fbbf24")'],
                    'background' => ['type' => 'string', 'description' => 'Custom CSS background or image path'],
                    'visible' => ['type' => 'boolean', 'description' => 'Public visibility toggle'],
                    'sort_order' => ['type' => 'integer', 'description' => 'Grid display rank (default 100)']
                ],
                'required' => ['name', 'title', 'summary']
            ]
        ],
        [
            'name' => 'translate_tile',
            'description' => 'Translate an existing tile metadata and HTML content into the target language ("en" or "de") using AI translation.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Name of the tile to translate'],
                    'target_lang' => ['type' => 'string', 'enum' => ['de', 'en'], 'description' => 'Target language to translate into']
                ],
                'required' => ['name', 'target_lang']
            ]
        ],
        [
            'name' => 'list_translation_status',
            'description' => 'Retrieve matrix of tile translation statuses across languages and identify stale translations.',
            'inputSchema' => [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string', 'description' => 'Optional tile name filter']
                ]
            ]
        ]
    ];
}

// -------------------------------------------------------------------------
// 3. MCP Tool Dispatcher
// -------------------------------------------------------------------------
function execute_mcp_tool($tool_name, $args) {
    $db = get_db_connection();

    switch ($tool_name) {
        case 'search_tiles':
            $q = $args['q'] ?? null;
            $lang = $args['lang'] ?? 'de';
            $limit = isset($args['limit']) ? (int)$args['limit'] : 20;
            $offset = isset($args['offset']) ? (int)$args['offset'] : 0;
            return search_tiles($lang, $q, $offset, $limit);

        case 'get_similar_tiles':
            $name = $args['name'] ?? '';
            $lang = $args['lang'] ?? 'de';
            $limit = isset($args['limit']) ? (int)$args['limit'] : 5;
            $offset = isset($args['offset']) ? (int)$args['offset'] : 0;
            if (empty($name)) {
                throw new Exception("Tile name is required for similarity search.");
            }
            return get_similar_tiles($name, $lang, $limit, $offset);

        case 'get_tile':
            $name = $args['name'] ?? '';
            $lang = $args['lang'] ?? 'de';
            $include_content = $args['include_content'] ?? true;

            $stmt = $db->prepare("SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, accent_color, background, sort_order, created_at, updated_at FROM tiles WHERE name = :name AND language = :lang");
            $stmt->execute([':name' => $name, ':lang' => $lang]);
            $tile = $stmt->fetch();
            if (!$tile) {
                // Try fallback language
                $fallback = ($lang === 'de') ? 'en' : 'de';
                $stmt->execute([':name' => $name, ':lang' => $fallback]);
                $tile = $stmt->fetch();
            }
            if (!$tile) {
                throw new Exception("Tile '{$name}' not found.");
            }

            if ($include_content && !empty($tile['content_file'])) {
                $fpath = __DIR__ . '/contents/' . $tile['content_file'];
                if (file_exists($fpath)) {
                    $tile['article_html'] = file_get_contents($fpath);
                }
            }
            return $tile;

        case 'create_tile':
            $name = trim($args['name'] ?? '');
            $title = trim($args['title'] ?? '');
            $language = strtolower(trim($args['language'] ?? 'de'));
            $summary = trim($args['summary'] ?? '');
            $html_teaser = trim($args['html_teaser'] ?? '');
            $link = trim($args['link'] ?? '');
            $type = trim($args['type'] ?? 'doc');
            $content_file = trim($args['content_file'] ?? '');
            $accent_color = trim($args['accent_color'] ?? '#fbbf24');
            $background = trim($args['background'] ?? '');
            $visible = isset($args['visible']) ? (bool)$args['visible'] : true;
            $sort_order = isset($args['sort_order']) ? (int)$args['sort_order'] : 100;
            $tags_arr = (array)($args['tags'] ?? []);
            $pg_tags = '{' . implode(',', array_map('trim', $tags_arr)) . '}';

            if (empty($name) || empty($title) || empty($summary)) {
                throw new Exception("Name, title, and summary are required.");
            }

            $check = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :language");
            $check->execute([':name' => $name, ':language' => $language]);
            if ($check->fetch()) {
                throw new Exception("Card '{$name}' with language '{$language}' already exists.");
            }

            $doc_text = format_tile_document_text($name, $language, $tags_arr, $summary);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
            }

            if (!empty($content_file) && !empty($html_teaser)) {
                if (preg_match('/^[a-zA-Z0-9_\-\.]+\.html$/', $content_file)) {
                    file_put_contents(__DIR__ . '/contents/' . $content_file, $html_teaser);
                }
            }

            $sql = "
                INSERT INTO tiles (
                    name, language, tags, title, html_teaser,
                    summary, link, type, content_file,
                    visible, accent_color, background, embedding, sort_order, created_at, updated_at
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser,
                    :summary, :link, :type, :content_file,
                    :visible, :accent_color, :background, :embedding::vector, :sort_order,
                    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                ) RETURNING id
            ";
            $stmt = $db->prepare($sql);
            $stmt->execute([
                ':name' => $name,
                ':language' => $language,
                ':tags' => $pg_tags,
                ':title' => $title,
                ':html_teaser' => $html_teaser,
                ':summary' => $summary,
                ':link' => $link ?: null,
                ':type' => $type,
                ':content_file' => $content_file ?: null,
                ':visible' => $visible,
                ':accent_color' => $accent_color,
                ':background' => $background ?: null,
                ':embedding' => $vector_str,
                ':sort_order' => $sort_order
            ]);
            $new_id = $stmt->fetchColumn();

            return [
                'id' => (int)$new_id,
                'name' => $name,
                'language' => $language,
                'title' => $title,
                'summary' => $summary,
                'tags' => $tags_arr,
                'content_file' => $content_file ?: null
            ];

        case 'translate_tile':
            $name = trim($args['name'] ?? '');
            $target_lang = strtolower(trim($args['target_lang'] ?? 'en'));
            if (empty($name) || empty($target_lang)) {
                throw new Exception("Name and target_lang are required.");
            }

            $source_lang = ($target_lang === 'en') ? 'de' : 'en';
            $stmt = $db->prepare("SELECT * FROM tiles WHERE name = :name AND language = :source_lang");
            $stmt->execute([':name' => $name, ':source_lang' => $source_lang]);
            $source_tile = $stmt->fetch();
            if (!$source_tile) {
                throw new Exception("Source tile '{$name}' in language '{$source_lang}' not found.");
            }

            $contents_dir = __DIR__ . '/contents/';
            $source_content = '';
            if (!empty($source_tile['content_file'])) {
                $fpath = $contents_dir . $source_tile['content_file'];
                if (file_exists($fpath)) {
                    $source_content = file_get_contents($fpath);
                }
            }
            if (empty($source_content)) {
                $source_content = $source_tile['html_teaser'];
            }

            $src_tags = $source_tile['tags'] ?? '';
            $raw_tags = trim((string)$src_tags, '{}');
            $tags_arr = array_filter(array_map('trim', explode(',', $raw_tags)));

            $lang_full_name = ($target_lang === 'en') ? 'English' : 'German';
            $meta_prompt = "You are an expert bilingual content translator. Translate the following tile metadata from " . strtoupper($source_lang) . " into {$lang_full_name}.
Respond ONLY with a valid JSON object matching this structure (no markdown, no backticks):
{
  \"title\": \"Translated Title\",
  \"summary\": \"Translated summary...\",
  \"tags\": [\"tag1\", \"tag2\"]
}";
            $meta_user = "Source Title: {$source_tile['title']}\nSource Tags: " . implode(', ', $tags_arr) . "\nSource Summary: {$source_tile['summary']}";

            $meta_response = trim(call_llm($meta_prompt, $meta_user));
            if (strpos($meta_response, '```') !== false) {
                $meta_response = trim(preg_replace('/^```(?:json)?|```$/m', '', $meta_response));
            }
            $translated_meta = json_decode($meta_response, true);
            if (json_last_error() !== JSON_ERROR_NONE) {
                throw new Exception("LLM metadata translation failed: " . $meta_response);
            }

            $html_prompt = "You are an expert HTML translator. Translate all human-readable text in the provided HTML snippet into {$lang_full_name}.
Keep all HTML tags, structure, classes, IDs, icons (<i class=\"...\"></i>), and attributes intact.
Respond ONLY with the translated HTML string.";

            $translated_html = trim(call_llm($html_prompt, $source_content));
            if (strpos($translated_html, '```') !== false) {
                $translated_html = trim(preg_replace('/^```(?:html|xml|json)?|```$/m', '', $translated_html));
            }

            $target_content_file = null;
            if (!empty($source_tile['content_file'])) {
                $base_file = pathinfo($source_tile['content_file'], PATHINFO_FILENAME);
                $base_clean = preg_replace('/[_\-](de|en)$/i', '', $base_file);
                $target_content_file = "{$base_clean}_{$target_lang}.html";
                file_put_contents($contents_dir . $target_content_file, $translated_html);
            }

            $new_tags = $translated_meta['tags'] ?? $tags_arr;
            $new_summary = $translated_meta['summary'] ?? $source_tile['summary'];
            $pg_tags = '{' . implode(',', array_map('trim', $new_tags)) . '}';

            $doc_text = format_tile_document_text($name, $target_lang, $new_tags, $new_summary);
            $vector_str = null;
            try {
                $embedding = get_embedding($doc_text, 'document');
                $vector_str = array_to_postgres_vector($embedding);
            } catch (Exception $e) {
                $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
            }

            $check_stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :target_lang");
            $check_stmt->execute([':name' => $name, ':target_lang' => $target_lang]);
            $existing_target = $check_stmt->fetch();

            if ($existing_target) {
                $sql = "
                    UPDATE tiles 
                    SET tags = :tags, title = :title, html_teaser = :html_teaser,
                        summary = :summary, link = :link, type = :type,
                        content_file = :content_file, visible = :visible, accent_color = :accent_color,
                        background = :background, embedding = :embedding::vector, sort_order = :sort_order,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = :id
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':id', $existing_target['id'], PDO::PARAM_INT);
            } else {
                $sql = "
                    INSERT INTO tiles (
                        name, language, tags, title, html_teaser,
                        summary, link, type, content_file,
                        visible, accent_color, background, embedding, sort_order, created_at, updated_at
                    ) VALUES (
                        :name, :language, :tags, :title, :html_teaser,
                        :summary, :link, :type, :content_file,
                        :visible, :accent_color, :background, :embedding::vector, :sort_order,
                        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                    )
                ";
                $stmt = $db->prepare($sql);
                $stmt->bindValue(':language', $target_lang, PDO::PARAM_STR);
            }

            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':tags', $pg_tags, PDO::PARAM_STR);
            $stmt->bindValue(':title', $translated_meta['title'], PDO::PARAM_STR);
            $stmt->bindValue(':html_teaser', $translated_html, PDO::PARAM_STR);
            $stmt->bindValue(':summary', $new_summary, PDO::PARAM_STR);
            $stmt->bindValue(':link', $source_tile['link'], PDO::PARAM_STR);
            $stmt->bindValue(':type', $source_tile['type'], PDO::PARAM_STR);
            $stmt->bindValue(':content_file', $target_content_file, PDO::PARAM_STR);
            $stmt->bindValue(':visible', (bool)$source_tile['visible'], PDO::PARAM_BOOL);
            $stmt->bindValue(':accent_color', $source_tile['accent_color'], PDO::PARAM_STR);
            $stmt->bindValue(':background', $source_tile['background'], PDO::PARAM_STR);
            $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
            $stmt->bindValue(':sort_order', (int)$source_tile['sort_order'], PDO::PARAM_INT);
            $stmt->execute();

            return [
                'name' => $name,
                'source_language' => $source_lang,
                'target_language' => $target_lang,
                'title' => $translated_meta['title'],
                'summary' => $new_summary,
                'tags' => $new_tags,
                'content_file' => $target_content_file
            ];

        case 'list_translation_status':
            $tile_name = $args['name'] ?? null;
            return check_translation_matrix($tile_name);

        default:
            throw new Exception("Unknown tool '{$tool_name}'.");
    }
}

// -------------------------------------------------------------------------
// 4. JSON-RPC Message Receiver (POST /mcp.php?action=message)
// -------------------------------------------------------------------------
header('Content-Type: application/json; charset=utf-8');

$rawInput = file_get_contents('php://input');
if (empty($rawInput)) {
    $rawInput = file_get_contents('php://stdin');
}
$request = json_decode($rawInput, true);

if (!is_array($request) || !isset($request['jsonrpc'])) {
    http_response_code(400);
    echo json_encode([
        'jsonrpc' => '2.0',
        'id' => null,
        'error' => ['code' => -32600, 'message' => 'Invalid JSON-RPC Request']
    ]);
    exit;
}

$id = $request['id'] ?? null;
$method = $request['method'] ?? '';
$params = $request['params'] ?? [];

$response = null;

try {
    switch ($method) {
        case 'initialize':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'protocolVersion' => '2024-11-05',
                    'capabilities' => [
                        'tools' => (object)[]
                    ],
                    'serverInfo' => [
                        'name' => 'leben-mcp-server',
                        'version' => '1.0.0'
                    ]
                ]
            ];
            break;

        case 'notifications/initialized':
            http_response_code(202);
            exit;

        case 'tools/list':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'tools' => get_mcp_tools()
                ]
            ];
            break;

        case 'tools/call':
            $tool_name = $params['name'] ?? '';
            $arguments = $params['arguments'] ?? [];
            
            $tool_result = execute_mcp_tool($tool_name, $arguments);
            
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => [
                    'content' => [
                        [
                            'type' => 'text',
                            'text' => json_encode($tool_result, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE)
                        ]
                    ]
                ]
            ];
            break;

        case 'ping':
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'result' => (object)[]
            ];
            break;

        default:
            $response = [
                'jsonrpc' => '2.0',
                'id' => $id,
                'error' => [
                    'code' => -32601,
                    'message' => "Method '{$method}' not found."
                ]
            ];
            break;
    }
} catch (Exception $e) {
    $response = [
        'jsonrpc' => '2.0',
        'id' => $id,
        'result' => [
            'content' => [
                [
                    'type' => 'text',
                    'text' => json_encode(['error' => $e->getMessage()], JSON_UNESCAPED_UNICODE)
                ]
            ],
            'isError' => true
        ]
    ];
}

// Queue message for active SSE listener if sessionId present
if (!empty($sessionId)) {
    $queueFile = get_session_queue_file($sessionId);
    if (file_exists($queueFile)) {
        $raw = file_get_contents($queueFile);
        $queue = json_decode($raw, true) ?: [];
        $queue[] = $response;
        file_put_contents($queueFile, json_encode($queue));
    }
}

// Return direct JSON-RPC HTTP response
echo json_encode($response, JSON_UNESCAPED_UNICODE);
